package factory

import (
	"fmt"
	"time"
)

// RunState is the terminal or in-flight state of a factory run.
//
// The states are deliberately explicit: the MVP's most important property is
// that agent outcome and verification outcome are tracked SEPARATELY, so
// "the agent did something" can never be confused with "the change is good".
type RunState string

const (
	// StateRequested means the request was accepted and the workflow started.
	StateRequested RunState = "REQUESTED"
	// StateRunning means the workflow is executing.
	StateRunning RunState = "RUNNING"
	// StatePaused means the run is waiting on a human review gate. The sandbox
	// may be checkpointed (SuspendResult reports whether it actually was). The
	// run is NOT finished: it resumes, or the gate times out and it continues
	// down the normal path.
	StatePaused RunState = "PAUSED"
	// StateAgentFailed means the coding agent exited non-zero or timed out.
	StateAgentFailed RunState = "AGENT_FAILED"
	// StateVerificationFailed means the agent reported success but a mandatory
	// deterministic gate failed. This is the state that proves the factory does
	// not trust the model.
	StateVerificationFailed RunState = "VERIFICATION_FAILED"
	// StateSucceeded means every mandatory gate passed.
	StateSucceeded RunState = "SUCCEEDED"
	// StateCancelled means the workflow was cancelled.
	StateCancelled RunState = "CANCELLED"
	// StateInfrastructureFailed means the factory could not complete the run
	// for a non-agent reason: Cube unreachable, provision failed, clone failed.
	StateInfrastructureFailed RunState = "INFRASTRUCTURE_FAILED"
	// StateInvalidRequest means the request was rejected by operator policy
	// before any sandbox was created: a disallowed repository, an unknown
	// harness, a missing revision.
	StateInvalidRequest RunState = "INVALID_REQUEST"
)

// Valid reports whether s is a known state.
func (s RunState) Valid() bool {
	switch s {
	case StateRequested, StateRunning, StatePaused, StateAgentFailed, StateVerificationFailed,
		StateSucceeded, StateCancelled, StateInfrastructureFailed, StateInvalidRequest:
		return true
	default:
		return false
	}
}

// Outcome is the result of one stage (agent, verification, cleanup).
type Outcome string

const (
	OutcomeSuccess   Outcome = "SUCCESS"
	OutcomeFailed    Outcome = "FAILED"
	OutcomeTimedOut  Outcome = "TIMED_OUT"
	OutcomeCancelled Outcome = "CANCELLED"
	OutcomeSkipped   Outcome = "SKIPPED"
	OutcomeError     Outcome = "ERROR"
)

// CleanupResult records whether the sandbox was actually destroyed.
//
// A leaked VM is an integration failure, so this is first-class evidence rather
// than a log line.
type CleanupResult struct {
	Attempted  bool          `json:"attempted"`
	Outcome    Outcome       `json:"outcome"`
	SandboxID  string        `json:"sandbox_id,omitempty"`
	Duration   time.Duration `json:"duration"`
	Verified   bool          `json:"verified"`
	Remaining  int           `json:"remaining_sandboxes"`
	Error      string        `json:"error,omitempty"`
	FinishedAt time.Time     `json:"finished_at"`
}

// PreviewLink is an ingress URL exposed for human review.
//
// It is evidence, not configuration: publishing a port is a deliberate act by
// an operator, and the record of it belongs with the run.
type PreviewLink struct {
	Port int    `json:"port"`
	URL  string `json:"url"`
}

// RunManifest is the durable record of one factory run.
//
// Field groups below the MVP fields are the explicit Phase 2 extension points
// required by the design: speculative execution needs candidate identity,
// snapshot lineage, model accounting and human review outcome, and adding those
// fields later must not require rewriting the manifest.
type RunManifest struct {
	// ── Identity ────────────────────────────────────────────────────────────
	RunID          string `json:"run_id"`
	FactoryVersion string `json:"factory_version"`
	WorkflowID     string `json:"workflow_id"`
	WorkflowRunID  string `json:"workflow_run_id"`

	// ── Durable work item ───────────────────────────────────────────────────
	//
	// A run is an activity of a Change. A run with no ChangeID is its own
	// change, which preserves the one-run-per-task behaviour.
	ChangeID string `json:"change_id,omitempty"`
	// ParentRunID is the run this one reworks, when a review asked for changes.
	ParentRunID string `json:"parent_run_id,omitempty"`
	// Scope is the tenancy boundary the run executed under.
	Scope string `json:"scope,omitempty"`

	// ── Input ───────────────────────────────────────────────────────────────
	Repository        string `json:"repository"`
	RepositoryKind    string `json:"repository_kind,omitempty"`
	RequestedRevision string `json:"requested_revision"`
	BaselineSHA       string `json:"baseline_sha,omitempty"`
	ResultingSHA      string `json:"resulting_sha_if_any,omitempty"`
	TaskHash          string `json:"task_hash"`

	// ── Execution environment ───────────────────────────────────────────────
	AgentHarness        string `json:"agent_harness"`
	AgentVersion        string `json:"agent_version_if_available,omitempty"`
	SandboxProvider     string `json:"sandbox_provider"`
	CubeVersion         string `json:"cube_version_if_available,omitempty"`
	SandboxTemplate     string `json:"sandbox_template"`
	SandboxID           string `json:"sandbox_id,omitempty"`
	VerificationProfile string `json:"verification_profile"`

	// ── Resolved resources (AX-style) ───────────────────────────────────────
	//
	// Digests, not just names, so a run is reproducible from its own evidence:
	// "workspace=default" is not reproducible, "workspace=default@sha256:..."
	// is. The credential VALUE is never recorded, only the variable name.
	Workspace       string `json:"workspace,omitempty"`
	WorkspaceDigest string `json:"workspace_digest,omitempty"`
	EgressPolicy    string `json:"egress_policy,omitempty"`
	EgressDigest    string `json:"egress_digest,omitempty"`
	EgressSummary   string `json:"egress_summary,omitempty"`
	ModelDigest     string `json:"model_digest,omitempty"`
	ResourceDigest  string `json:"resource_digest,omitempty"`

	// ── Lifecycle ───────────────────────────────────────────────────────────
	//
	// Conditions are the per-step outcomes. They exist because a single run
	// state cannot answer "which step failed" without reading Temporal history.
	Conditions []Condition `json:"conditions,omitempty"`
	// SuspendResult records whether the review gate actually checkpointed the
	// sandbox. It is OutcomeSkipped when the provider has no suspend capability,
	// which is the difference between "free while paused" and "live but idle".
	SuspendResult Outcome `json:"suspend_result,omitempty"`
	SuspendError  string  `json:"suspend_error,omitempty"`
	// PreviewResult records whether requested review previews were published,
	// skipped for lack of capability, or failed.
	PreviewResult Outcome `json:"preview_result,omitempty"`
	// ReviewConfigured reports that a human gate was part of this run, so an
	// operator can tell "reviewed and approved" from "no review was configured".
	ReviewConfigured bool `json:"review_configured,omitempty"`
	// ReviewOutcome records how the gate ended: approved, rejected, or timeout.
	ReviewOutcome string `json:"review_outcome,omitempty"`
	// ReviewNote is the redacted human instruction attached to a rejection. It
	// is carried into the durable Change as its rework note.
	ReviewNote string `json:"review_note,omitempty"`
	// ReviewError records a non-fatal review-gate problem, such as a preview
	// port that could not be published. It is kept separate from SuspendError so
	// "the sandbox was not checkpointed" and "a preview was unavailable" are
	// never conflated.
	ReviewError string `json:"review_error,omitempty"`
	// Previews are the ingress URLs exposed for human review, recorded as
	// evidence because they were a deliberate exposure of the sandbox.
	Previews []PreviewLink `json:"previews,omitempty"`
	// BudgetExceeded lists the ceilings this run crossed, if any.
	BudgetExceeded []string `json:"budget_exceeded,omitempty"`

	// ── Timing ──────────────────────────────────────────────────────────────
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at"`
	Duration    time.Duration `json:"duration"`

	// ── Results (kept separate on purpose) ──────────────────────────────────
	AgentResult        Outcome       `json:"agent_result"`
	AgentExitCode      int           `json:"agent_exit_code"`
	VerificationResult Outcome       `json:"verification_result"`
	FactoryResult      RunState      `json:"factory_result"`
	CleanupResult      CleanupResult `json:"cleanup_result"`
	// Intake is operator-visible advice and never changes FactoryResult.
	Intake *IntakeResult `json:"intake,omitempty"`

	// ── Evidence pointers (store-relative) ──────────────────────────────────
	Artifacts string `json:"artifacts"`
	Patch     string `json:"patch,omitempty"`
	Error     string `json:"error,omitempty"`

	// VerificationMutatedTree reports that a verification gate changed the
	// working tree. The deliverable patch is captured BEFORE verification, so
	// such a change is never attributed to the agent; it is recorded separately
	// as evidence that the gate is not side-effect free.
	VerificationMutatedTree bool   `json:"verification_mutated_tree"`
	PostVerificationPatch   string `json:"post_verification_patch,omitempty"`

	// ── Phase 2 extension points (unused in the MVP) ────────────────────────
	Model          string   `json:"model,omitempty"`
	ModelProvider  string   `json:"model_provider,omitempty"`
	TokensIn       *int64   `json:"tokens_in,omitempty"`
	TokensOut      *int64   `json:"tokens_out,omitempty"`
	InferenceCost  *float64 `json:"inference_cost,omitempty"`
	SnapshotID     string   `json:"snapshot_id,omitempty"`
	CloneParent    string   `json:"clone_parent,omitempty"`
	CandidateID    string   `json:"candidate_id,omitempty"`
	Strategy       string   `json:"strategy,omitempty"`
	EvaluatorScore *float64 `json:"evaluator_score,omitempty"`
	ReviewFindings []string `json:"review_findings,omitempty"`
	HumanResult    string   `json:"human_result,omitempty"`
}

// Artifact paths, relative to a run's artifact directory.
//
// They are constants so the CLI, the operator guide and the tests cannot drift.
const (
	ArtifactTask               = "task.json"
	ArtifactRun                = "run.json"
	ArtifactEnvironment        = "environment.json"
	ArtifactInventory          = "inventory.json"
	ArtifactGitBefore          = "git-before.txt"
	ArtifactGitAfter           = "git-after.txt"
	ArtifactPatch              = "changes.patch"
	ArtifactCleanup            = "cleanup.json"
	ArtifactAgentStdout        = "agent/stdout.log"
	ArtifactAgentStderr        = "agent/stderr.log"
	ArtifactAgentResult        = "agent/result.json"
	ArtifactAgentPrompt        = "agent/prompt.txt"
	ArtifactVerificationResult = "verification/result.json"
	ArtifactBaseline           = "baseline.json"
	// ArtifactAudit is the append-only audit trail for operator actions that
	// touch a live sandbox (attach, preview). It exists because those actions
	// are privileged and must be attributable after the fact.
	ArtifactAudit = "audit/actions.jsonl"
	// ArtifactPostVerificationPatch records the repository state AFTER the
	// deterministic gates ran. If it is non-empty, a gate mutated the working
	// tree — recorded as evidence rather than silently folded into the
	// deliverable, which is captured before verification.
	ArtifactPostVerificationPatch  = "verification/post-verification.patch"
	ArtifactPostVerificationStatus = "verification/post-verification.status"
	ArtifactManifestDir            = "."
)

// RunRequest is the input to a factory run.
//
// Everything here is either operator policy or the small, explicitly allowed
// set of user-controlled values: task text, an approved repository, and an
// approved revision.
type RunRequest struct {
	RunID      string `json:"run_id"`
	Repository string `json:"repository"`
	// RepositoryKind is "remote" or "local".
	RepositoryKind string `json:"repository_kind,omitempty"`
	// LocalPath is a host path, used by tests and by the acceptance fixture.
	LocalPath string `json:"local_path,omitempty"`
	Revision  string `json:"revision"`
	Task      string `json:"task"`

	SandboxTemplate     string `json:"sandbox_template"`
	AgentHarness        string `json:"agent_harness"`
	AgentModel          string `json:"agent_model,omitempty"`
	VerificationProfile string `json:"verification_profile"`

	// ── Declarative resources (AX-style) ────────────────────────────────────
	//
	// These name operator-declared resources. A caller may only name what the
	// scope it runs under grants; it can never define a resource, and an empty
	// name falls back to the scope's default rather than to "no policy".

	// Scope selects the tenancy boundary. Empty means the default scope.
	Scope string `json:"scope,omitempty"`
	// Workspace names a pre-warmed execution environment.
	Workspace string `json:"workspace,omitempty"`
	// Model names a model endpoint, overriding the harness default.
	Model string `json:"model,omitempty"`
	// EgressPolicy names the network policy the sandbox runs under.
	EgressPolicy string `json:"egress_policy,omitempty"`
	// Review, when set, overrides the factory-wide review gate for this run.
	// A pointer is used so "unset" inherits instead of silently disabling.
	Review *bool `json:"review,omitempty"`

	// ── Durable work item ───────────────────────────────────────────────────

	// ChangeID links this run to a durable Change. Empty creates a new change
	// whose identity is the run itself, which keeps the MVP's one-run-per-task
	// behaviour working unchanged.
	ChangeID string `json:"change_id,omitempty"`
	// ParentRunID records the run this one reworks, when a review asked for
	// changes. It is lineage evidence, never an authority.
	ParentRunID string `json:"parent_run_id,omitempty"`

	// Timeouts.
	AgentTimeout        time.Duration `json:"agent_timeout"`
	VerificationTimeout time.Duration `json:"verification_timeout"`
	TotalTimeout        time.Duration `json:"total_timeout"`

	// RepositoryDir is where the repository is prepared inside the sandbox.
	RepositoryDir string `json:"repository_dir,omitempty"`
}

// Validate rejects a request that the factory cannot honour.
func (r RunRequest) Validate() error {
	var problems []string
	if r.RunID == "" {
		problems = append(problems, "run_id is required")
	}
	if r.Task == "" {
		problems = append(problems, "task is required")
	}
	if r.Repository == "" && r.LocalPath == "" {
		problems = append(problems, "a repository is required")
	}
	if r.Revision == "" {
		problems = append(problems, "revision is required (runs must be reproducible)")
	}
	if r.AgentHarness == "" {
		problems = append(problems, "agent_harness is required")
	}
	if r.VerificationProfile == "" {
		problems = append(problems, "verification_profile is required")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid run request: %v", problems)
	}
	return nil
}

// DefaultRepositoryDir is where repositories are prepared inside a sandbox.
const DefaultRepositoryDir = "/workspace/repository"

// EffectiveRepositoryDir returns the configured or default repository path.
func (r RunRequest) EffectiveRepositoryDir() string {
	if r.RepositoryDir != "" {
		return r.RepositoryDir
	}
	return DefaultRepositoryDir
}
