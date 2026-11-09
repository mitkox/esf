package factory

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mitkox/esf/internal/evidence"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/notify"
	"github.com/mitkox/esf/internal/secretfile"
	"github.com/mitkox/esf/internal/verification"
)

// maxEvidenceSigningKeyBytes bounds the private key file. An ed25519 PKCS#8
// key is tiny; a large file is a configuration mistake or an attack.
const maxEvidenceSigningKeyBytes = 16 << 10

// evidenceSigningVersion gates the post-finalize signing step so workflow
// histories recorded before the step replay down the old path.
const evidenceSigningVersion = "evidence-signing-v1"

// SignEvidenceInput identifies the finalized run to sign.
type SignEvidenceInput struct {
	RunID string `json:"run_id"`
}

// SignEvidenceOutput records what the signer wrote. Configured is false when
// the operator has not supplied a key, which is the documented disabled state.
type SignEvidenceOutput struct {
	Configured        bool   `json:"configured"`
	BundleArtifact    string `json:"bundle_artifact,omitempty"`
	SignatureArtifact string `json:"signature_artifact,omitempty"`
}

// RecordEvidenceFailureInput records a signing failure on the durable manifest.
type RecordEvidenceFailureInput struct {
	RunID      string      `json:"run_id"`
	Error      string      `json:"error"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// evidenceSignature is the detached signature artifact. The bundle is already
// a signed DSSE envelope; this sidecar lets a consumer attach the signature
// separately without reimplementing the envelope's JSON shape.
type evidenceSignature struct {
	PayloadType string               `json:"payloadType"`
	Signatures  []evidence.Signature `json:"signatures"`
}

// SignEvidence builds, signs and writes the run's change-evidence bundle.
//
// It runs on the host after the run manifest is durable. Every input comes from
// the artifact store, so a file the agent wrote inside the repository can never
// become evidence. The private key is read through the private-file rules and
// never leaves this activity.
func (a *Activities) SignEvidence(ctx context.Context, in SignEvidenceInput) (SignEvidenceOutput, error) {
	if !a.cfg.Evidence.configured() {
		return SignEvidenceOutput{}, nil
	}
	if strings.TrimSpace(in.RunID) == "" {
		return SignEvidenceOutput{}, fmt.Errorf("evidence: a run id is required")
	}

	keyBody, err := secretfile.Read(a.cfg.Evidence.SigningKeyFile, maxEvidenceSigningKeyBytes)
	if err != nil {
		return SignEvidenceOutput{}, fmt.Errorf("evidence: read signing key: %w", err)
	}
	privateKey, err := evidence.ParsePrivateKey(keyBody)
	if err != nil {
		return SignEvidenceOutput{}, err
	}

	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		return SignEvidenceOutput{}, fmt.Errorf("evidence: open artifact store: %w", err)
	}
	manifest, err := ReadManifest(a.artifactFactory, in.RunID)
	if err != nil {
		return SignEvidenceOutput{}, err
	}
	record, err := a.evidenceRecord(ctx, store, manifest)
	if err != nil {
		return SignEvidenceOutput{}, err
	}
	envelope, err := evidence.Build(record, evidence.Ed25519Signer{Key: privateKey})
	if err != nil {
		return SignEvidenceOutput{}, err
	}
	// The signature is written first so a bundle can never appear without the
	// sidecar that accompanies it. A signature without a bundle is inert.
	sidecar := evidenceSignature{PayloadType: envelope.PayloadType, Signatures: envelope.Signatures}
	if err := a.writeJSON(store, ArtifactEvidenceSignature, sidecar); err != nil {
		return SignEvidenceOutput{}, fmt.Errorf("evidence: write signature: %w", err)
	}
	if err := a.writeJSON(store, ArtifactEvidenceBundle, envelope); err != nil {
		return SignEvidenceOutput{}, fmt.Errorf("evidence: write bundle: %w", err)
	}
	// Redaction is applied on the way to storage. Verify the bytes that landed,
	// so a scrub that altered the signed payload cannot be reported as evidence.
	if err := verifyPersistedBundle(store, privateKey); err != nil {
		return SignEvidenceOutput{}, err
	}

	a.log.InfoContext(ctx, "evidence signed",
		slog.String("run.id", manifest.RunID),
		slog.String("change.id", record.ChangeID),
		slog.String("bundle", ArtifactEvidenceBundle))
	return SignEvidenceOutput{
		Configured:        true,
		BundleArtifact:    ArtifactEvidenceBundle,
		SignatureArtifact: ArtifactEvidenceSignature,
	}, nil
}

// RecordEvidenceFailure rewrites the durable manifest with the evidence-failure
// result. It exists because signing necessarily happens after the manifest is
// written, so a signing failure cannot be described by that first write.
func (a *Activities) RecordEvidenceFailure(ctx context.Context, in RecordEvidenceFailureInput) (FinalizeOutput, error) {
	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		return FinalizeOutput{}, err
	}
	manifest, err := ReadManifest(a.artifactFactory, in.RunID)
	if err != nil {
		return FinalizeOutput{}, err
	}
	manifest.FactoryResult = StateEvidenceFailed
	manifest.Error = a.redactor.Redact(in.Error)
	manifest.Conditions = in.Conditions
	changeID := strings.TrimSpace(manifest.ChangeID)
	if changeID == "" {
		changeID = manifest.RunID
	}
	if metrics := a.telemetry.Metrics(); metrics != nil {
		metrics.RecordEvidenceFailure(ctx, "signing_failed")
	}
	if !manifestAlertRecorded(manifest, AlertEvidenceFailed) {
		manifest = a.appendManifestAlert(ctx, manifest, a.raiseAlert(ctx, manifest.RunID, AlertEvidenceFailed, notify.SeverityCritical,
			"evidence signing failed", map[string]string{"change": changeID, "error": manifest.Error}))
	}
	if err := a.writeJSON(store, ArtifactRun, manifest); err != nil {
		return FinalizeOutput{}, err
	}
	a.log.ErrorContext(ctx, "evidence signing failed",
		slog.String("run.id", in.RunID),
		slog.String("error", manifest.Error))
	return FinalizeOutput{Manifest: manifest}, nil
}

// verifyPersistedBundle checks the stored envelope against the signing key's
// public half. It catches a storage or redaction step that changed signed
// bytes, which would otherwise leave an unverifiable bundle recorded as success.
func verifyPersistedBundle(store artifacts.Store, privateKey ed25519.PrivateKey) error {
	body, err := store.Read(ArtifactEvidenceBundle)
	if err != nil {
		return fmt.Errorf("evidence: read back bundle: %w", err)
	}
	var envelope evidence.Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("evidence: decode persisted bundle: %w", err)
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("evidence: signing key has no ed25519 public half")
	}
	if _, err := evidence.Verify(&envelope, publicKey); err != nil {
		return fmt.Errorf("evidence: persisted bundle does not verify: %w", err)
	}
	return nil
}

// evidenceRecord assembles the bundle record from the durable manifest, the
// change record and the artifacts that record the executed gates.
//
// The change index is derived evidence, so an unavailable record cannot fail a
// successful run: the bundle falls back to the change identity the manifest
// already implies.
func (a *Activities) evidenceRecord(ctx context.Context, store artifacts.Store, manifest RunManifest) (evidence.Record, error) {
	changeID := manifest.ChangeID
	if changeID == "" {
		// A run with no explicit change is its own change, matching the rule
		// used when the change was recorded.
		changeID = manifest.RunID
	}
	if changes, err := NewChangeStore(a.artifactFactory.Root()); err == nil {
		if change, err := changes.Load(changeID); err == nil {
			changeID = change.ChangeID
		} else {
			a.log.WarnContext(ctx, "change record unavailable for evidence",
				slog.String("change.id", changeID), slog.String("error", err.Error()))
		}
	} else {
		a.log.WarnContext(ctx, "change store unavailable for evidence",
			slog.String("change.id", changeID), slog.String("error", err.Error()))
	}

	patchPath := manifest.Patch
	if patchPath == "" {
		patchPath = ArtifactPatch
	}
	patch, err := store.Read(patchPath)
	if err != nil {
		return evidence.Record{}, fmt.Errorf("evidence: read patch: %w", err)
	}

	verified, err := readVerificationResult(store)
	if err != nil {
		return evidence.Record{}, err
	}
	gates, err := evidenceGates(store, verified)
	if err != nil {
		return evidence.Record{}, err
	}

	return evidence.Record{
		RunID:             manifest.RunID,
		WorkflowID:        manifest.WorkflowID,
		ChangeID:          changeID,
		ParentRunID:       manifest.ParentRunID,
		FactoryVersion:    manifest.FactoryVersion,
		State:             string(manifest.FactoryResult),
		StartedAt:         manifest.StartedAt,
		CompletedAt:       manifest.CompletedAt,
		Repository:        manifest.Repository,
		RequestedRevision: manifest.RequestedRevision,
		BaseCommit:        manifest.BaselineSHA,
		ResultCommit:      manifest.ResultingSHA,
		TaskHash:          manifest.TaskHash,
		Patch:             patch,
		Agent: evidence.Agent{
			Harness:         manifest.AgentHarness,
			Version:         manifest.AgentVersion,
			Model:           manifest.Model,
			SandboxProvider: manifest.SandboxProvider,
			Image:           manifest.SandboxTemplate,
		},
		Verification: evidence.Verification{
			Profile: verified.Profile,
			Passed:  verified.Passed,
			Gates:   gates,
		},
		PolicyDigest: manifest.ResourceDigest,
	}, nil
}

func readVerificationResult(store artifacts.Store) (verification.Result, error) {
	body, err := store.Read(ArtifactVerificationResult)
	if err != nil {
		return verification.Result{}, fmt.Errorf("evidence: read verification result: %w", err)
	}
	var result verification.Result
	if err := json.Unmarshal(body, &result); err != nil {
		return verification.Result{}, fmt.Errorf("evidence: decode verification result: %w", err)
	}
	return result, nil
}

func evidenceGates(store artifacts.Store, verified verification.Result) ([]evidence.Gate, error) {
	gates := make([]evidence.Gate, 0, len(verified.Steps))
	for _, step := range verified.Steps {
		gate := evidence.Gate{
			Name:     step.ID,
			Command:  append([]string(nil), step.Argv...),
			Passed:   step.Passed,
			TimedOut: step.TimedOut,
			ExitCode: step.ExitCode,
			Duration: step.Duration,
		}
		var err error
		if step.StdoutArtifact != "" {
			if gate.StdoutDigest, err = artifactDigest(store, step.StdoutArtifact); err != nil {
				return nil, err
			}
		}
		if step.StderrArtifact != "" {
			if gate.StderrDigest, err = artifactDigest(store, step.StderrArtifact); err != nil {
				return nil, err
			}
		}
		gates = append(gates, gate)
	}
	return gates, nil
}

func artifactDigest(store artifacts.Store, relPath string) (string, error) {
	body, err := store.Read(relPath)
	if err != nil {
		return "", fmt.Errorf("evidence: read gate log %q: %w", relPath, err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// manifestAlertRecorded reports whether this alert kind is already durable, so
// a retried activity updates the manifest instead of appending a duplicate.
func manifestAlertRecorded(manifest RunManifest, kind string) bool {
	if manifest.Hardening == nil {
		return false
	}
	for _, alert := range manifest.Hardening.Alerts {
		if alert.Kind == kind {
			return true
		}
	}
	return false
}
