package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// This file holds the AX-inspired lifecycle activities: the in-sandbox
// inventory, sandbox suspend/resume, preview exposure, and change recording.
//
// They are separate activities rather than inlined workflow code for the same
// reason every other side effect is: Temporal retries them, they appear in
// workflow history, and the workflow stays deterministic.

// ── Inventory ───────────────────────────────────────────────────────────────

// WriteInventoryInput writes the harness-facing environment document.
type WriteInventoryInput struct {
	RunID       string            `json:"run_id"`
	SandboxID   string            `json:"sandbox_id"`
	Request     RunRequest        `json:"request"`
	Resources   ResolvedResources `json:"resources"`
	BaselineSHA string            `json:"baseline_sha,omitempty"`
	Template    string            `json:"template,omitempty"`
}

// WriteInventoryOutput records where the inventory landed and its digest.
type WriteInventoryOutput struct {
	// Path is the in-sandbox path the harness contract promises.
	Path string `json:"path"`
	// Digest is the SHA-256 of the exact bytes written, so the evidence copy and
	// the agent-visible copy are provably the same document.
	Digest        string        `json:"digest"`
	Duration      time.Duration `json:"duration"`
	EvidenceError string        `json:"evidence_error,omitempty"`
}

// WriteInventory writes the inventory into the sandbox and persists the same
// bytes as evidence.
//
// A failure here fails the run: the inventory is part of the harness contract,
// and continuing without it would mean an agent silently running blind while
// the factory claimed the environment was described.
func (a *Activities) WriteInventory(ctx context.Context, in WriteInventoryInput) (WriteInventoryOutput, error) {
	started := time.Now()
	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return WriteInventoryOutput{}, err
	}
	inventory := BuildInventory(in.Request, in.Resources, InventoryInput{
		FactoryVersion: a.cfg.effectiveVersion(),
		SandboxID:      in.SandboxID,
		Template:       in.Template,
		Provider:       a.provider.Name(),
		BaselineSHA:    in.BaselineSHA,
		WrittenAt:      time.Now().UTC(),
	})
	repositoryDir := in.Request.EffectiveRepositoryDir()
	data, err := WriteInventoryDocument(ctx, sb, repositoryDir, inventory)
	if err != nil {
		return WriteInventoryOutput{}, err
	}
	sum := sha256.Sum256(data)
	out := WriteInventoryOutput{
		Path:     InventoryPathFor(repositoryDir),
		Digest:   "sha256:" + hex.EncodeToString(sum[:]),
		Duration: time.Since(started),
	}
	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		out.EvidenceError = err.Error()
		return out, nil
	}
	if err := store.Write(ArtifactInventory, data); err != nil {
		out.EvidenceError = err.Error()
	}
	a.log.InfoContext(ctx, "sandbox inventory written",
		slog.String("run.id", in.RunID),
		slog.String("inventory.path", out.Path),
		slog.String("inventory.digest", out.Digest))
	return out, nil
}

// ── Suspend / resume ────────────────────────────────────────────────────────

// SandboxLifecycleInput targets one sandbox for a lifecycle action.
type SandboxLifecycleInput struct {
	RunID     string `json:"run_id"`
	SandboxID string `json:"sandbox_id"`
	// Reason is recorded in the audit trail and in evidence.
	Reason string `json:"reason,omitempty"`
}

// SandboxLifecycleOutput reports what happened.
//
// Outcome is SKIPPED when the provider has no capability for the action. That
// is reported rather than hidden: "paused but still paying for the VM" and
// "paused and free" are different operational facts.
type SandboxLifecycleOutput struct {
	Outcome  Outcome       `json:"outcome"`
	Error    string        `json:"error,omitempty"`
	Duration time.Duration `json:"duration"`
}

// SuspendSandbox checkpoints an idle sandbox.
//
// A provider without the capability returns SKIPPED with a reason, and a failed
// checkpoint returns an error so Temporal retries it. The caller decides
// whether an exhausted retry is fatal; it is not, because a paused run with a
// live sandbox is still reviewable.
func (a *Activities) SuspendSandbox(ctx context.Context, in SandboxLifecycleInput) (SandboxLifecycleOutput, error) {
	started := time.Now()
	suspender, ok := a.provider.(sandbox.Suspender)
	if !ok || !a.provider.Capabilities().Suspend {
		out := SandboxLifecycleOutput{
			Outcome:  OutcomeSkipped,
			Error:    "sandbox provider does not support suspend",
			Duration: time.Since(started),
		}
		a.auditLifecycle(ctx, in, AuditSuspend, out)
		return out, nil
	}
	if err := suspender.Suspend(ctx, in.SandboxID); err != nil {
		out := SandboxLifecycleOutput{
			Outcome:  OutcomeFailed,
			Error:    a.redactor.Redact(err.Error()),
			Duration: time.Since(started),
		}
		a.auditLifecycle(ctx, in, AuditSuspend, out)
		return out, fmt.Errorf("suspend sandbox %s: %w", in.SandboxID, err)
	}
	out := SandboxLifecycleOutput{Outcome: OutcomeSuccess, Duration: time.Since(started)}
	a.auditLifecycle(ctx, in, AuditSuspend, out)
	return out, nil
}

// ResumeSandbox wakes a checkpointed sandbox.
//
// Unlike suspend, a failure here is serious: every later activity needs the
// sandbox, so the error propagates and the caller records an infrastructure
// failure rather than pretending the run can continue.
func (a *Activities) ResumeSandbox(ctx context.Context, in SandboxLifecycleInput) (SandboxLifecycleOutput, error) {
	started := time.Now()
	suspender, ok := a.provider.(sandbox.Suspender)
	if !ok || !a.provider.Capabilities().Resume {
		out := SandboxLifecycleOutput{
			Outcome:  OutcomeSkipped,
			Error:    "sandbox provider does not support resume",
			Duration: time.Since(started),
		}
		a.auditLifecycle(ctx, in, AuditResume, out)
		return out, nil
	}
	if err := suspender.Resume(ctx, in.SandboxID); err != nil {
		out := SandboxLifecycleOutput{
			Outcome:  OutcomeFailed,
			Error:    a.redactor.Redact(err.Error()),
			Duration: time.Since(started),
		}
		a.auditLifecycle(ctx, in, AuditResume, out)
		return out, fmt.Errorf("resume sandbox %s: %w", in.SandboxID, err)
	}
	out := SandboxLifecycleOutput{Outcome: OutcomeSuccess, Duration: time.Since(started)}
	a.auditLifecycle(ctx, in, AuditResume, out)
	return out, nil
}

// ── Preview ─────────────────────────────────────────────────────────────────

// PreviewInput asks for ingress URLs for ports inside a sandbox.
type PreviewInput struct {
	RunID     string `json:"run_id"`
	SandboxID string `json:"sandbox_id"`
	Ports     []int  `json:"ports"`
}

// PreviewOutput returns the links that were created and any port that could not
// be exposed.
//
// A preview is for a human, so a partial result is useful: failing the whole
// run because one convenience port is unavailable would be disproportionate.
type PreviewOutput struct {
	Outcome  Outcome       `json:"outcome"`
	Links    []PreviewLink `json:"links,omitempty"`
	Failures []string      `json:"failures,omitempty"`
}

// PreviewSandbox exposes ports through the deployment ingress.
//
// It is capability-gated exactly like suspend: a provider that cannot route to
// a sandbox reports that plainly instead of returning a URL that does not work.
func (a *Activities) PreviewSandbox(ctx context.Context, in PreviewInput) (PreviewOutput, error) {
	previewer, ok := a.provider.(sandbox.Previewer)
	if !ok || !a.provider.Capabilities().Preview {
		out := PreviewOutput{
			Outcome:  OutcomeSkipped,
			Failures: []string{"sandbox provider does not support preview"},
		}
		a.appendAudit(ctx, in.RunID, AuditEntry{
			Action:    AuditPreview,
			RunID:     in.RunID,
			SandboxID: in.SandboxID,
			Outcome:   string(OutcomeSkipped),
			Error:     out.Failures[0],
			Detail:    map[string]any{"ports": in.Ports},
		})
		return out, nil
	}
	out := PreviewOutput{Outcome: OutcomeSuccess}
	for _, port := range in.Ports {
		url, err := previewer.PreviewURL(ctx, in.SandboxID, port)
		if err != nil {
			out.Failures = append(out.Failures, fmt.Sprintf("port %d: %s", port, a.redactor.Redact(err.Error())))
			out.Outcome = OutcomeFailed
			continue
		}
		out.Links = append(out.Links, PreviewLink{Port: port, URL: url})
	}
	a.appendAudit(ctx, in.RunID, AuditEntry{
		Action:    AuditPreview,
		RunID:     in.RunID,
		SandboxID: in.SandboxID,
		Outcome:   auditOutcome(len(out.Links) > 0),
		Detail: map[string]any{
			"ports":   in.Ports,
			"links":   len(out.Links),
			"failure": strings.Join(out.Failures, "; "),
		},
	})
	return out, nil
}

// ── Change recording ────────────────────────────────────────────────────────

// RecordChangeInput folds a finished run into its durable Change.
type RecordChangeInput struct {
	RunID string `json:"run_id"`
}

// RecordChangeOutput reports the parent's state after the update.
type RecordChangeOutput struct {
	ChangeID string       `json:"change_id"`
	Attempts int          `json:"attempts"`
	Status   ChangeStatus `json:"status"`
	// TotalCostUSD and TotalTokens are aggregates over activations.
	TotalCostUSD float64 `json:"total_cost_usd"`
	TotalTokens  int64   `json:"total_tokens"`
	Error        string  `json:"error,omitempty"`
}

// RecordChange maintains the durable work item after the manifest is written.
//
// The manifest is read back from the artifact store rather than passed in: the
// change's aggregates must be derived from the same durable record an auditor
// reads, not from an in-memory value that could differ from it.
func (a *Activities) RecordChange(ctx context.Context, in RecordChangeInput) (RecordChangeOutput, error) {
	manifest, err := ReadManifest(a.artifactFactory, in.RunID)
	if err != nil {
		return RecordChangeOutput{}, fmt.Errorf("record change: read manifest: %w", err)
	}
	store, err := NewChangeStore(a.artifactFactory.Root())
	if err != nil {
		return RecordChangeOutput{}, err
	}
	change, err := store.RecordRun(manifest)
	if err != nil {
		return RecordChangeOutput{}, fmt.Errorf("record change: %w", err)
	}
	a.log.InfoContext(ctx, "change recorded",
		slog.String("change.id", change.ChangeID),
		slog.String("run.id", in.RunID),
		slog.Int("attempts", change.Attempts),
		slog.String("change.status", string(change.Status)))
	return RecordChangeOutput{
		ChangeID:     change.ChangeID,
		Attempts:     change.Attempts,
		Status:       change.Status,
		TotalCostUSD: change.TotalCostUSD,
		TotalTokens:  change.TotalTokens,
	}, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

// auditLifecycle records a suspend/resume action in the run's audit trail.
func (a *Activities) auditLifecycle(ctx context.Context, in SandboxLifecycleInput, action AuditAction, out SandboxLifecycleOutput) {
	a.appendAudit(ctx, in.RunID, AuditEntry{
		Action:    action,
		RunID:     in.RunID,
		SandboxID: in.SandboxID,
		Outcome:   string(out.Outcome),
		Detail:    map[string]any{"reason": in.Reason},
		Error:     out.Error,
	})
}

// appendAudit writes an audit entry, degrading to a log line when the evidence
// store is unavailable. An audit failure must never mask the action's outcome.
func (a *Activities) appendAudit(ctx context.Context, runID string, entry AuditEntry) {
	store, err := a.artifactFactory.ForRun(runID)
	if err != nil {
		a.log.WarnContext(ctx, "audit entry not persisted", slog.String("error", err.Error()))
		return
	}
	if err := AppendAudit(store, entry, a.redactor); err != nil {
		a.log.WarnContext(ctx, "audit entry not persisted", slog.String("error", err.Error()))
	}
}

// auditOutcome maps a success boolean onto the audit vocabulary.
func auditOutcome(ok bool) string {
	if ok {
		return string(OutcomeSuccess)
	}
	return string(OutcomeFailed)
}
