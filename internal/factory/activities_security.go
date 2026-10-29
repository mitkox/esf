package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/securityassessment"
	"go.temporal.io/sdk/workflow"
)

const (
	ArtifactSecurityAssessment  = "security/assessment.json"
	ArtifactSecurityManifest    = "security/manifest.json"
	SecurityQualificationSchema = "esf.security-qualification/v1"
	securityAssessmentVersion   = "security-assessment-advisory-v1"
)

var securityDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var securityName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type SecurityAssessmentProfileConfig struct {
	ProfilePath         string `toml:"profile_path" json:"profile_path"`
	ProfileSHA256       string `toml:"profile_sha256" json:"profile_sha256"`
	QualificationPath   string `toml:"qualification_path" json:"qualification_path"`
	QualificationSHA256 string `toml:"qualification_sha256" json:"qualification_sha256"`
}

type SecurityQualification struct {
	Schema          string                        `json:"schema"`
	RouteQualified  bool                          `json:"route_qualified"`
	CleanupVerified bool                          `json:"cleanup_verified"`
	Runtime         securityassessment.RuntimePin `json:"runtime"`
}

func (p SecurityAssessmentProfileConfig) validate() error {
	if !filepath.IsAbs(p.ProfilePath) || !filepath.IsAbs(p.QualificationPath) || !securityDigest.MatchString(p.ProfileSHA256) || !securityDigest.MatchString(p.QualificationSHA256) {
		return errors.New("security assessment requires absolute operator profile/qualification paths and SHA-256 pins")
	}
	return nil
}

func readSecurityPinned(name, expected string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, errors.New("security policy input unavailable")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || securityassessment.SHA256(data) != expected {
		return nil, errors.New("security policy input missing, oversized or changed")
	}
	return data, nil
}

func decodeSecurity(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return errors.New("invalid security policy schema")
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing security policy input")
	}
	return nil
}

func sameSecurityValue(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return bytes.Equal(left, right)
}

func (p SecurityAssessmentProfileConfig) load() (securityassessment.Profile, error) {
	if err := p.validate(); err != nil {
		return securityassessment.Profile{}, err
	}
	data, err := readSecurityPinned(p.ProfilePath, p.ProfileSHA256)
	if err != nil {
		return securityassessment.Profile{}, err
	}
	var profile securityassessment.Profile
	if err := decodeSecurity(data, &profile); err != nil {
		return profile, err
	}
	if err := profile.Validate(); err != nil {
		return profile, err
	}
	data, err = readSecurityPinned(p.QualificationPath, p.QualificationSHA256)
	if err != nil {
		return profile, err
	}
	var qualification SecurityQualification
	if err := decodeSecurity(data, &qualification); err != nil {
		return profile, err
	}
	if qualification.Schema != SecurityQualificationSchema || !qualification.RouteQualified || !qualification.CleanupVerified || !sameSecurityValue(qualification.Runtime, profile.Runtime) {
		return profile, errors.New("assessment runtime/route qualification is incomplete or mismatched")
	}
	return profile, nil
}

type SecurityAssessmentSummary struct {
	Profile          string `json:"profile"`
	Status           string `json:"status"`
	AssertionOutcome string `json:"assertion_outcome"`
	CandidateDigest  string `json:"candidate_digest,omitempty"`
	ManifestArtifact string `json:"manifest_artifact,omitempty"`
	ManifestSHA256   string `json:"manifest_sha256,omitempty"`
	CleanupStatus    string `json:"cleanup_status,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
}

// SecurityAssessmentExecution and its result stay inside an activity process;
// exact candidate and evidence bytes must never enter Temporal payloads.
type SecurityAssessmentExecution struct {
	RunID, ChangeID, ParentRunID, ProfileName string
	Profile                                   securityassessment.Profile
	Candidate                                 securityassessment.CandidateExport
}
type SecurityAssessmentExecutionResult struct {
	Profile         securityassessment.Profile
	Expected        securityassessment.Identity
	Manifest        securityassessment.Manifest
	PrivateEvidence map[string][]byte
	ExpectedHashes  map[string]string
}
type SecurityAssessmentExecutor interface {
	Assess(context.Context, SecurityAssessmentExecution) (SecurityAssessmentExecutionResult, error)
}

type SecurityCandidateReference struct {
	Ref             string `json:"ref,omitempty"`
	CandidateDigest string `json:"candidate_digest,omitempty"`
	ArchiveSHA256   string `json:"archive_sha256,omitempty"`
	ManifestSHA256  string `json:"manifest_sha256,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
}
type CaptureSecurityCandidateInput struct {
	RunID         string                          `json:"run_id"`
	SandboxID     string                          `json:"sandbox_id"`
	RepositoryDir string                          `json:"repository_dir"`
	ProfileConfig SecurityAssessmentProfileConfig `json:"profile_config"`
}
type SecurityAssessmentInput struct {
	RunID                   string                          `json:"run_id"`
	ChangeID                string                          `json:"change_id"`
	ParentRunID             string                          `json:"parent_run_id,omitempty"`
	SandboxID               string                          `json:"sandbox_id"`
	RepositoryDir           string                          `json:"repository_dir"`
	ProfileName             string                          `json:"profile_name"`
	ProfileConfig           SecurityAssessmentProfileConfig `json:"profile_config"`
	Snapshot                SecurityCandidateReference      `json:"snapshot"`
	FunctionalPassed        bool                            `json:"functional_passed"`
	FunctionalEvidenceError bool                            `json:"functional_evidence_error"`
	VerificationMutatedTree bool                            `json:"verification_mutated_tree"`
}
type SecurityAssessmentOutput struct {
	Summary       SecurityAssessmentSummary `json:"summary"`
	EvidenceError string                    `json:"evidence_error,omitempty"`
}

func (a *Activities) securityPrivateRoot(runID string) (*os.Root, error) {
	if !securityName.MatchString(runID) {
		return nil, errors.New("invalid security run identity")
	}
	base := a.cfg.Storage.DataDir
	if base == "" && a.artifactFactory != nil {
		base = a.artifactFactory.Root()
	}
	if base == "" {
		return nil, errors.New("private security storage unavailable")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	private := filepath.Join("security-private", runID)
	if err := root.MkdirAll(private, 0700); err != nil {
		return nil, err
	}
	if err := root.Chmod("security-private", 0700); err != nil {
		return nil, err
	}
	if err := root.Chmod(private, 0700); err != nil {
		return nil, err
	}
	return root.OpenRoot(private)
}

func (a *Activities) writeSecurityPrivate(runID, name string, data []byte) error {
	if filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "..") || len(data) > 80<<20 {
		return errors.New("invalid private security artifact")
	}
	root, err := a.securityPrivateRoot(runID)
	if err != nil {
		return err
	}
	defer root.Close()
	parent := filepath.Dir(name)
	if err := root.MkdirAll(parent, 0700); err != nil {
		return err
	}
	if err := root.Chmod(parent, 0700); err != nil {
		return err
	}
	tmp := filepath.Join(parent, ".assessment-"+fmt.Sprint(time.Now().UnixNano()))
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}

func (a *Activities) readSecurityPrivate(runID, name string, max int64) ([]byte, error) {
	root, err := a.securityPrivateRoot(runID)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, errors.New("private security artifact truncated or oversized")
	}
	return data, nil
}

func (a *Activities) CaptureSecurityCandidate(ctx context.Context, in CaptureSecurityCandidateInput) (out SecurityCandidateReference, returnErr error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()
	defer func() {
		if ctx.Err() != nil {
			out = SecurityCandidateReference{ErrorCode: "candidate_capture_incomplete"}
			returnErr = errors.Join(ctx.Err(), a.removeSecurityCandidate(in.RunID))
		}
	}()
	failed := func() (SecurityCandidateReference, error) {
		return SecurityCandidateReference{ErrorCode: "candidate_capture_incomplete"}, nil
	}
	if _, err := in.ProfileConfig.load(); err != nil {
		return failed()
	}
	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return failed()
	}
	// Leave room for the exporter's detached 30-second cleanup before the
	// two-minute Temporal activity deadline.
	exportCtx, stopExport := context.WithTimeout(ctx, time.Minute)
	defer stopExport()
	export, err := securityassessment.ExportCandidate(exportCtx, sb, in.RepositoryDir, securityassessment.DefaultCandidatePolicy())
	if err != nil {
		return failed()
	}
	if ctx.Err() != nil {
		return failed()
	}
	data, err := json.Marshal(export.Manifest)
	if err != nil {
		return failed()
	}
	if err := a.writeSecurityPrivate(in.RunID, "candidate/archive.tar", export.Archive); err != nil {
		return failed()
	}
	if ctx.Err() != nil {
		return failed()
	}
	if err := a.writeSecurityPrivate(in.RunID, "candidate/manifest.json", data); err != nil {
		return failed()
	}
	return SecurityCandidateReference{Ref: "candidate-v1", CandidateDigest: export.Manifest.Digest, ArchiveSHA256: export.Manifest.ArchiveSHA256, ManifestSHA256: securityassessment.SHA256(data)}, nil
}

func (a *Activities) persistSecuritySummary(runID string, out *SecurityAssessmentOutput) {
	if a.artifactFactory == nil {
		out.EvidenceError = "security_evidence_store_unavailable"
		out.Summary.Status = "incomplete"
		return
	}
	store, err := a.artifactFactory.ForRun(runID)
	if err != nil {
		out.EvidenceError = "security_evidence_store_unavailable"
		out.Summary.Status = "incomplete"
		return
	}
	data, err := json.MarshalIndent(out.Summary, "", "  ")
	if err == nil {
		err = store.Write(ArtifactSecurityAssessment, data)
	}
	if err != nil {
		out.EvidenceError = "security_advisory_persistence_failed"
		out.Summary.Status = "incomplete"
		out.Summary.AssertionOutcome = "unobserved"
	}
}

type RecordSecurityAssessmentInput struct {
	RunID   string                    `json:"run_id"`
	Summary SecurityAssessmentSummary `json:"summary"`
}

func (a *Activities) RecordSecurityAssessment(_ context.Context, in RecordSecurityAssessmentInput) (SecurityAssessmentOutput, error) {
	out := SecurityAssessmentOutput{Summary: in.Summary}
	if err := a.removeSecurityCandidate(in.RunID); err != nil {
		out.Summary.Status = "incomplete"
		out.Summary.AssertionOutcome = "unobserved"
		out.Summary.CleanupStatus = "incomplete"
		out.Summary.ErrorCode = "candidate_cleanup_incomplete"
	}
	a.persistSecuritySummary(in.RunID, &out)
	return out, nil
}

func (a *Activities) AssessSecurity(ctx context.Context, in SecurityAssessmentInput) (out SecurityAssessmentOutput, returnErr error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()
	if in.ChangeID == "" {
		in.ChangeID = in.RunID
	}
	out.Summary = SecurityAssessmentSummary{Profile: in.ProfileName, Status: "incomplete", AssertionOutcome: "unobserved", CandidateDigest: in.Snapshot.CandidateDigest}
	defer func() {
		if err := a.removeSecurityCandidate(in.RunID); err != nil {
			out.Summary.Status = "incomplete"
			out.Summary.AssertionOutcome = "unobserved"
			out.Summary.CleanupStatus = "incomplete"
			out.Summary.ErrorCode = "candidate_cleanup_incomplete"
		}
		a.persistSecuritySummary(in.RunID, &out)
		// Preserve cancellation only after the executor and private snapshot
		// cleanup have returned. Advisory failures otherwise remain non-gating.
		if ctx.Err() != nil {
			returnErr = ctx.Err()
		}
	}()
	profile, err := in.ProfileConfig.load()
	if err != nil {
		out.Summary.ErrorCode = "qualification_unavailable"
		return out, nil
	}
	if !in.FunctionalPassed {
		out.Summary.ErrorCode = "functional_verification_failed"
		return out, nil
	}
	if in.FunctionalEvidenceError {
		out.Summary.ErrorCode = "functional_evidence_incomplete"
		return out, nil
	}
	if in.VerificationMutatedTree {
		out.Summary.ErrorCode = "verification_mutated_candidate"
		return out, nil
	}
	if a.securityAssessor == nil {
		out.Summary.ErrorCode = "executor_unavailable"
		return out, nil
	}
	if in.Snapshot.Ref != "candidate-v1" || in.Snapshot.ErrorCode != "" {
		out.Summary.ErrorCode = "candidate_capture_incomplete"
		return out, nil
	}
	data, err := a.readSecurityPrivate(in.RunID, "candidate/manifest.json", 1<<20)
	if err != nil || securityassessment.VerifyEvidence(data, in.Snapshot.ManifestSHA256) != nil {
		out.Summary.ErrorCode = "candidate_snapshot_invalid"
		return out, nil
	}
	var before securityassessment.CandidateManifest
	if decodeSecurity(data, &before) != nil || before.Digest != in.Snapshot.CandidateDigest || before.ArchiveSHA256 != in.Snapshot.ArchiveSHA256 {
		out.Summary.ErrorCode = "candidate_snapshot_invalid"
		return out, nil
	}
	archive, err := a.readSecurityPrivate(in.RunID, "candidate/archive.tar", 80<<20)
	if err != nil || securityassessment.VerifyEvidence(archive, in.Snapshot.ArchiveSHA256) != nil {
		out.Summary.ErrorCode = "candidate_snapshot_invalid"
		return out, nil
	}
	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		out.Summary.ErrorCode = "candidate_unavailable"
		return out, nil
	}
	candidate, err := securityassessment.ExportCandidate(ctx, sb, in.RepositoryDir, securityassessment.DefaultCandidatePolicy())
	if err != nil || securityassessment.VerifyCandidateUnchanged(before, candidate.Manifest) != nil {
		out.Summary.ErrorCode = "candidate_changed"
		return out, nil
	}
	result, err := a.securityAssessor.Assess(ctx, SecurityAssessmentExecution{RunID: in.RunID, ChangeID: in.ChangeID, ParentRunID: in.ParentRunID, ProfileName: in.ProfileName, Profile: profile, Candidate: candidate})
	if err != nil {
		out.Summary.ErrorCode = "assessment_execution_incomplete"
		return out, nil
	}
	bound := profile
	bound.TargetURL = result.Profile.TargetURL
	if !sameSecurityValue(bound, result.Profile) || result.Profile.Validate() != nil || result.Expected.RunID != in.RunID || result.Expected.ChangeID != in.ChangeID || result.Expected.ParentRunID != in.ParentRunID || result.Expected.CandidateDigest != candidate.Manifest.Digest || !sameSecurityValue(result.Expected.Runtime, profile.Runtime) || !sameSecurityValue(result.Expected.RequiredChecks, result.Profile.RequiredChecks) || !sameSecurityValue(result.Expected.Limits, result.Profile.Limits) || !sameSecurityValue(result.Expected.AI, result.Profile.AI) {
		out.Summary.ErrorCode = "assessment_identity_mismatch"
		return out, nil
	}
	digest, err := result.Profile.Digest()
	if err != nil || digest != result.Expected.ProfileDigest {
		out.Summary.ErrorCode = "assessment_profile_mismatch"
		return out, nil
	}
	if err := result.Manifest.Verify(result.Expected, result.PrivateEvidence, result.ExpectedHashes); err != nil {
		out.Summary.ErrorCode = "assessment_evidence_incomplete"
		return out, nil
	}
	for _, evidence := range result.Manifest.Evidence {
		if err := a.writeSecurityPrivate(in.RunID, filepath.Join("evidence", evidence.Name), result.PrivateEvidence[evidence.Name]); err != nil {
			out.Summary.ErrorCode = "assessment_evidence_persistence_failed"
			return out, nil
		}
	}
	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		out.Summary.ErrorCode = "assessment_manifest_persistence_failed"
		return out, nil
	}
	manifest, err := json.MarshalIndent(result.Manifest, "", "  ")
	if err != nil || store.Write(ArtifactSecurityManifest, manifest) != nil {
		out.Summary.ErrorCode = "assessment_manifest_persistence_failed"
		return out, nil
	}
	out.Summary.ManifestArtifact = ArtifactSecurityManifest
	out.Summary.ManifestSHA256 = securityassessment.SHA256(manifest)
	out.Summary.CleanupStatus = string(result.Manifest.Cleanup.Status)
	complete := !result.Manifest.Truncated && result.Manifest.Cleanup.Status == securityassessment.Complete && !result.Manifest.Cleanup.PendingAllocation && len(result.Manifest.Cleanup.RemainingIDs) == 0
	for _, coverage := range result.Manifest.Coverage {
		complete = complete && coverage.Status == securityassessment.Complete
	}
	var observed bool
	out.Summary.AssertionOutcome, observed = securityAssertionOutcome(result.Manifest.Assertions)
	complete = complete && observed
	if complete {
		out.Summary.Status = "complete"
	} else {
		out.Summary.ErrorCode = "assessment_coverage_incomplete"
	}
	return out, nil
}

func securityAssertionOutcome(assertions []securityassessment.Assertion) (string, bool) {
	failed, unobserved := false, false
	for _, assertion := range assertions {
		failed = failed || assertion.Status == securityassessment.Failed
		unobserved = unobserved || assertion.Status == securityassessment.Unobserved
	}
	if failed {
		return "failed", !unobserved
	}
	if unobserved {
		return "unobserved", false
	}
	return "passed", true
}

func (a *Activities) removeSecurityCandidate(runID string) error {
	root, err := a.securityPrivateRoot(runID)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll("candidate")
}

func securityAssessmentOptions() workflow.ActivityOptions {
	opts := noRetry()
	opts.StartToCloseTimeout = 17 * time.Minute
	// Heartbeats deliver cancellation to the executor; its detached teardown
	// must finish before workflow finalization can acknowledge cancellation.
	opts.HeartbeatTimeout = 3 * time.Minute
	opts.WaitForCancellation = true
	return opts
}

func securityCandidateCaptureOptions() workflow.ActivityOptions {
	opts := noRetry()
	opts.StartToCloseTimeout = 2 * time.Minute
	opts.HeartbeatTimeout = time.Minute
	opts.WaitForCancellation = true
	return opts
}
