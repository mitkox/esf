package factory

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/notify"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/threatmon"
	"github.com/mitkox/esf/internal/verification"
)

// TaskQueue is the default Temporal task queue for factory activities.
const TaskQueue = "factory"

// WorkflowName is the registered workflow type.
const WorkflowName = "SoftwareChangeWorkflow"

// WorkflowIDPrefix namespaces factory workflows inside a Temporal namespace.
const WorkflowIDPrefix = "factory-run-"

// WorkflowIDForRun derives the workflow ID for a run.
//
// It is deterministic so the CLI can locate a run's workflow without a separate
// registry, and so resubmitting the same run ID is idempotent.
func WorkflowIDForRun(runID string) string { return WorkflowIDPrefix + runID }

// RunIDFromWorkflowID reverses WorkflowIDForRun.
func RunIDFromWorkflowID(workflowID string) (string, bool) {
	if !strings.HasPrefix(workflowID, WorkflowIDPrefix) {
		return "", false
	}
	return strings.TrimPrefix(workflowID, WorkflowIDPrefix), true
}

// RunStatus is the workflow's queryable status.
//
// It is deliberately small: it is returned by a Temporal query while the
// workflow is still running, and the full record lives in the run manifest.
type RunStatus struct {
	RunID         string        `json:"run_id"`
	State         RunState      `json:"state"`
	SandboxID     string        `json:"sandbox_id,omitempty"`
	CurrentStep   string        `json:"current_step"`
	StartedAt     time.Time     `json:"started_at"`
	BaselineSHA   string        `json:"baseline_sha,omitempty"`
	AgentOutcome  Outcome       `json:"agent_result"`
	VerifyOutcome Outcome       `json:"verification_result"`
	Intake        *IntakeResult `json:"intake,omitempty"`
	Error         string        `json:"error,omitempty"`
	SandboxGone   bool          `json:"sandbox_destroyed"`
	// ChangeID is the durable work item this run belongs to.
	ChangeID string `json:"change_id,omitempty"`
	// Scope is the tenancy boundary the run executes under.
	Scope string `json:"scope,omitempty"`
	// Conditions are the per-step outcomes observed so far. A watcher can
	// follow them without waiting for the final manifest.
	Conditions []Condition `json:"conditions,omitempty"`
}

// workflowState is the deterministic state carried across workflow steps.
//
// Every field is derived from an activity result, never from wall-clock time or
// randomness, so Temporal replay reconstructs it identically.
type workflowState struct {
	status          RunStatus
	executionID     string
	createAttempted bool
	totalTimedOut   bool

	validate          ValidateOutput
	intake            *IntakeResult
	resources         ResolvedResources
	baseline          repository.Baseline
	sandboxID         string
	template          string
	harnessVer        string
	inventoryPath     string
	inventoryDigest   string
	agent             agentharness.Result
	agentOutcome      Outcome
	agentRan          bool
	agentAttmpt       int32
	verify            verification.Result
	verifyRan         bool
	patch             string
	patchPath         string
	postPatchPath     string
	verificationDrift bool
	resultingSHA      string
	cleanup           CleanupResult
	cleanupDone       bool
	collectionFailed  bool

	// ── AX-inspired lifecycle ───────────────────────────────────────────────
	// suspendOutcome records whether the review gate actually checkpointed the
	// sandbox; suspendError explains a non-fatal failure to do so.
	suspendOutcome Outcome
	suspendError   string
	previewOutcome Outcome
	// resumeFailed is fatal: every later activity needs a live sandbox.
	resumeFailed bool
	// reviewOutcome is approved, rejected or timeout.
	reviewOutcome   string
	reviewNote      string
	reviewRequested bool
	// reviewError holds a non-fatal gate problem (a preview that could not be
	// published); suspendError stays specific to the checkpoint.
	reviewError    string
	previews       []PreviewLink
	reviewPorts    []int
	budgetExceeded []string
	changeID       string
	changeRecorded bool

	// ── Hardening ───────────────────────────────────────────────────────────
	// Every field here is derived from an activity result, so it is
	// deterministic under replay.
	egressProbe   *EgressProbeResult
	behavior      *threatmon.Report
	behaviorError string
	gateIntegrity *GateIntegrity
	blocked       bool
	blockedReason string
	alerts        []AlertRecord
}

// SoftwareChangeWorkflow is the factory's durable run:
//
//	validate → cube.create → sandbox.prepare → repo.prepare →
//	agent.run → verify → artifacts.collect → finalize
//
// with cleanup running on every exit path.
//
// The shape is deliberately linear for the MVP: one request, one sandbox, one
// agent, one deterministic verification, one patch. Speculative fan-out is a
// Phase 2 concern — but because every external operation is already an
// activity, fan-out can be added without restructuring this function.
//
// No network, filesystem or process call happens here: that is what keeps the
// workflow deterministic and therefore replayable.
func SoftwareChangeWorkflow(ctx workflow.Context, req RunRequest) (RunManifest, error) {
	// A zero-valued receiver identifies activity methods. It is never invoked
	// from workflow code; only its method name is used for dispatch.
	acts := &Activities{}
	info := workflow.GetInfo(ctx)
	startedAt := workflow.Now(ctx)

	state := &workflowState{
		executionID: info.WorkflowExecution.RunID,
		changeID:    req.ChangeID,
		status: RunStatus{
			RunID:         req.RunID,
			ChangeID:      req.ChangeID,
			Scope:         req.Scope,
			State:         StateRunning,
			CurrentStep:   "starting",
			StartedAt:     startedAt,
			AgentOutcome:  OutcomeSkipped,
			VerifyOutcome: OutcomeSkipped,
		},
	}
	if state.changeID == "" {
		// A run with no explicit change is its own change. This preserves the
		// MVP's one-run-per-task behaviour while giving every run a durable
		// parent that rework can attach to later.
		state.changeID = req.RunID
	}

	// Register the status query before any work so a caller can observe an
	// in-flight run.
	if err := workflow.SetQueryHandler(ctx, "status", func() (RunStatus, error) {
		return state.status, nil
	}); err != nil {
		return RunManifest{}, err
	}

	// Cleanup is registered BEFORE the sandbox exists, so it runs on every exit
	// path: success, agent failure, verification failure, timeout and
	// cancellation. A leaked microVM counts as a failed integration test, so
	// this is not best-effort even when the workflow is being cancelled.
	//
	// The defer is a SAFETY NET. The explicit call below runs first, because
	// the manifest is written after cleanup and must therefore contain the
	// cleanup result: a run whose manifest says cleanup was never attempted is
	// not auditable. cleanupSandbox is idempotent, so calling it twice is safe.
	defer func() { cleanupSandbox(ctx, acts, state) }()

	if err := state.run(ctx, acts, req); err != nil {
		state.status.Error = err.Error()
		if temporal.IsCanceledError(err) || ctx.Err() != nil {
			state.status.State = StateCancelled
		}
		if state.totalTimedOut {
			state.status.State = StateInfrastructureFailed
			state.status.Error = "total execution timeout exceeded"
		}
	}

	// Destroy the sandbox and verify it is gone BEFORE recording the result.
	state.status.CurrentStep = "cube.destroy"
	cleanupSandbox(ctx, acts, state)
	if state.cleanup.Verified {
		state.setCondition(ctx, ConditionCleanupVerified, ConditionTrue, "NoSandboxesRemain",
			fmt.Sprintf("remaining=%d", state.cleanup.Remaining))
	} else {
		state.setCondition(ctx, ConditionCleanupVerified, ConditionFalse, "CleanupUnverified",
			state.cleanup.Error)
	}
	state.status.CurrentStep = "finalizing"
	// Success requires both durable evidence and verified sandbox teardown.
	if state.status.State == StateSucceeded && (state.collectionFailed || !state.cleanup.Verified) {
		state.status.State = StateInfrastructureFailed
		if state.status.Error == "" {
			state.status.Error = "sandbox cleanup was not verified: " + state.cleanup.Error
		}
	}

	manifest, err := finalizeRun(ctx, acts, state, req, info, startedAt)
	if err != nil {
		return manifest, fmt.Errorf("finalize run: %w", err)
	}

	// EvidenceWritten is set AFTER the manifest is durable, so it is a
	// status-only condition: it tells a watcher the record landed, and it is
	// deliberately not part of the manifest it describes (the manifest's own
	// existence is the evidence).
	state.setCondition(ctx, ConditionEvidenceWritten, ConditionTrue, "ManifestPersisted",
		"artifacts="+manifest.Artifacts)

	// Fold the finished run into its durable work item. This is best-effort by
	// design: the manifest is the authoritative record of the execution, and a
	// failure to update a derived index must not turn a successful change into
	// a failed one. The gap surfaces as a missing entry in `factory get
	// changes` and in the worker log.
	state.changeRecorded = recordChange(ctx, acts, state)

	state.status.State = manifest.FactoryResult
	state.status.AgentOutcome = manifest.AgentResult
	state.status.VerifyOutcome = manifest.VerificationResult
	return manifest, nil
}

// setCondition records a lifecycle condition using workflow time.
//
// Conditions are set in workflow code, never in activities, so a replayed
// workflow reconstructs exactly the same list and timestamps.
func (s *workflowState) setCondition(ctx workflow.Context, t ConditionType, status ConditionStatus, reason, message string) {
	s.status.Conditions = SetCondition(s.status.Conditions, Condition{
		Type:               t,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: workflow.Now(ctx),
	})
}

// recordChange updates the durable Change after the manifest is written.
//
// It returns false when the index could not be updated; the run's own result is
// unaffected, because a derived index is not the source of truth.
func recordChange(ctx workflow.Context, acts *Activities, state *workflowState) bool {
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	})
	var out RecordChangeOutput
	if err := workflow.ExecuteActivity(actx, acts.RecordChange, RecordChangeInput{RunID: state.status.RunID}).Get(ctx, &out); err != nil {
		workflow.GetLogger(ctx).Warn("change record not updated",
			"run_id", state.status.RunID, "change_id", state.changeID, "error", err)
		return false
	}
	state.changeID = out.ChangeID
	return true
}

// run executes the workflow body, recording outcome state as it proceeds.
func (s *workflowState) run(ctx workflow.Context, acts *Activities, req RunRequest) error {
	// ── 1. Validate ─────────────────────────────────────────────────────────
	// Validation is deterministic, so it is not retried: a rejected request
	// will be rejected again.
	s.status.CurrentStep = "validate"
	var validated ValidateOutput
	if err := executeActivity(ctx, acts.ValidateRequest, noRetry(), ValidateInput{Request: req}).Get(ctx, &validated); err != nil {
		s.status.State = StateInvalidRequest
		s.setCondition(ctx, ConditionValidated, ConditionFalse, "PolicyRejected", err.Error())
		return fmt.Errorf("request rejected: %w", err)
	}
	s.validate = validated
	s.resources = validated.Resources
	s.reviewPorts = validated.Resources.PreviewPorts
	s.setCondition(ctx, ConditionValidated, ConditionTrue, "PolicyAccepted",
		"scope="+s.resources.Scope)
	req.AgentTimeout = validated.AgentTimeout
	req.VerificationTimeout = validated.VerificationTimeout
	req.TotalTimeout = validated.TotalTimeout
	ctx, cancel := workflow.WithCancel(ctx)
	defer cancel()
	workflow.Go(ctx, func(timerCtx workflow.Context) {
		if workflow.NewTimer(timerCtx, req.TotalTimeout).Get(timerCtx, nil) == nil {
			s.totalTimedOut = true
			cancel()
		}
	})

	// Intake is host-side advice only. A provider or activity failure must not
	// change a validated task's path through the factory.
	if workflow.GetVersion(ctx, "intake-advisory", workflow.DefaultVersion, 1) != workflow.DefaultVersion && validated.IntakeEnabled {
		s.status.CurrentStep = "intake.assess"
		intakeOpts := noRetry()
		intakeOpts.StartToCloseTimeout = 70 * time.Second
		var intake IntakeResult
		if err := executeActivity(ctx, acts.AssessIntake, intakeOpts, IntakeInput{Task: req.Task}).Get(ctx, &intake); err != nil {
			intake = IntakeResult{Status: "unavailable", ErrorCode: "activity_failed"}
		}
		s.intake = &intake
		s.status.Intake = &intake
	}

	// ── 2. Create sandbox ───────────────────────────────────────────────────
	s.status.CurrentStep = "cube.create"
	s.createAttempted = true
	// A resolved Workspace may override the deployment template. It can never
	// introduce one the operator did not declare, because the workspace itself
	// is operator configuration.
	template := s.resources.WorkspaceConfig.Template
	if template == "" {
		// Compatibility with workflow histories created before validation began
		// carrying the fully resolved workspace.
		template = req.SandboxTemplate
	}
	var created CreateSandboxOutput
	if err := executeActivity(ctx, acts.CreateSandbox, infraRetry(), CreateSandboxInput{
		ExecutionID: s.executionID,
		RunID:       req.RunID,
		Template:    template,
		Harness:     req.AgentHarness,
		IdleTimeout: req.TotalTimeout,
		Network:     s.resources.Egress,
	}).Get(ctx, &created); err != nil {
		s.status.State = StateInfrastructureFailed
		s.setCondition(ctx, ConditionSandboxReady, ConditionFalse, "CreateFailed", err.Error())
		return fmt.Errorf("create sandbox: %w", err)
	}
	s.sandboxID = created.SandboxID
	s.template = created.Template
	s.status.SandboxID = created.SandboxID
	s.setCondition(ctx, ConditionSandboxReady, ConditionTrue, "Created",
		"sandbox="+created.SandboxID+" template="+created.Template)

	// ── 3. Provision the coding agent ───────────────────────────────────────
	s.status.CurrentStep = "sandbox.prepare"
	var prepared PrepareSandboxOutput
	if err := executeActivity(ctx, acts.PrepareSandbox, infraRetry(), PrepareSandboxInput{
		RunID:             req.RunID,
		SandboxID:         s.sandboxID,
		Harness:           req.AgentHarness,
		BasePackages:      s.resources.WorkspaceConfig.BasePackages,
		SetupScript:       s.resources.WorkspaceConfig.SetupScript,
		WorkspaceResolved: true,
		ModelEndpoint: agentharness.ModelEndpoint{
			Provider:  s.resources.ModelProvider,
			Model:     s.resources.ModelID,
			BaseURL:   s.resources.ModelBaseURL,
			APIKeyEnv: s.resources.ModelAPIKeyEnv,
		},
	}).Get(ctx, &prepared); err != nil {
		s.status.State = StateInfrastructureFailed
		s.setCondition(ctx, ConditionWorkspacePrepared, ConditionFalse, "ProvisionFailed", err.Error())
		return fmt.Errorf("prepare sandbox: %w", err)
	}
	s.harnessVer = prepared.HarnessVersion
	s.setCondition(ctx, ConditionWorkspacePrepared, ConditionTrue, "Provisioned",
		"workspace="+s.resources.Workspace+" digest="+s.resources.WorkspaceDigest)

	// ── 4. Prepare the repository ───────────────────────────────────────────
	s.status.CurrentStep = "repo.prepare"
	var preparedRepo PrepareRepositoryOutput
	if err := executeActivity(ctx, acts.PrepareRepository, infraRetry(), PrepareRepositoryInput{
		RunID:     req.RunID,
		SandboxID: s.sandboxID,
		Request:   req,
	}).Get(ctx, &preparedRepo); err != nil {
		s.status.State = StateInfrastructureFailed
		s.setCondition(ctx, ConditionRepositoryPrepared, ConditionFalse, "PrepareFailed", err.Error())
		return fmt.Errorf("prepare repository: %w", err)
	}
	s.baseline = preparedRepo.Baseline
	s.status.BaselineSHA = preparedRepo.Baseline.SHA
	s.setCondition(ctx, ConditionRepositoryPrepared, ConditionTrue, "CheckedOut",
		"sha="+preparedRepo.Baseline.SHA)

	// ── 4b. Publish the sandbox inventory ──────────────────────────────────
	// The inventory is written AFTER the repository is prepared, because it
	// records the resolved SHA, and BEFORE the agent runs, because the harness
	// contract promises the agent can discover its environment. A failure here
	// is fatal: continuing would mean the agent runs blind while the manifest
	// claimed the environment was described.
	s.status.CurrentStep = "inventory.write"
	var inventory WriteInventoryOutput
	if err := executeActivity(ctx, acts.WriteInventory, infraRetry(), WriteInventoryInput{
		RunID:       req.RunID,
		SandboxID:   s.sandboxID,
		Request:     req,
		Resources:   s.resources,
		BaselineSHA: preparedRepo.Baseline.SHA,
		Template:    created.Template,
	}).Get(ctx, &inventory); err != nil {
		s.status.State = StateInfrastructureFailed
		s.setCondition(ctx, ConditionInventoryWritten, ConditionFalse, "WriteFailed", err.Error())
		return fmt.Errorf("write sandbox inventory: %w", err)
	}
	s.inventoryPath = inventory.Path
	s.inventoryDigest = inventory.Digest
	if inventory.EvidenceError != "" {
		s.collectionFailed = true
		s.status.Error = "inventory evidence persistence failed: " + inventory.EvidenceError
	}
	s.setCondition(ctx, ConditionInventoryWritten, ConditionTrue, "Published",
		"path="+inventory.Path+" digest="+inventory.Digest)

	// ── 5. Lock down runtime egress and then MEASURE it ────────────────────
	// The lockdown is a request to the provider. The probe is the factory's
	// evidence about what actually happened. If the two disagree, the run stops
	// before untrusted code executes: a boundary that is believed but not in
	// effect is worse than a boundary that is known to be absent.
	s.status.CurrentStep = "sandbox.network.lockdown"
	var lockdown ApplyRuntimeNetworkOutput
	lockdownErr := executeActivity(ctx, acts.ApplyRuntimeNetwork, infraRetry(), ApplyRuntimeNetworkInput{
		RunID:      req.RunID,
		SandboxID:  s.sandboxID,
		Harness:    req.AgentHarness,
		ExpectDeny: !s.resources.Egress.AllowsInternet(),
		Policy:     s.resources.EgressPolicy,
	}).Get(ctx, &lockdown)
	if lockdownErr != nil {
		s.status.State = StateEgressUnverified
		s.setCondition(ctx, ConditionEgressVerified, ConditionFalse, "ProbeFailed", lockdownErr.Error())
		s.recordAlert(ctx, acts, req, AlertEgressViolated, notify.SeverityCritical,
			"sandbox egress could not be verified", map[string]string{"error": lockdownErr.Error()})
		s.collect(ctx, acts, req, "")
		return fmt.Errorf("verify sandbox egress: %w", lockdownErr)
	}
	if probe := lockdown.Probe; probe != nil {
		s.egressProbe = probe
		if lockdown.ProbeEvidenceError != "" {
			// The measurement happened but its durable record did not. That is
			// an evidence failure like any other: a run may not report SUCCEEDED
			// while its boundary evidence is missing.
			s.collectionFailed = true
			s.status.Error = "egress probe evidence persistence failed: " + lockdown.ProbeEvidenceError
		}
		if len(probe.Violations) > 0 {
			// Fail closed: the boundary the run depends on is not the boundary
			// the factory believed it had. The agent never runs.
			detail := strings.Join(probe.Violations, "; ")
			s.status.State = StateEgressUnverified
			s.setCondition(ctx, ConditionEgressVerified, ConditionFalse, "BoundaryViolated", detail)
			s.recordAlert(ctx, acts, req, AlertEgressViolated, notify.SeverityCritical,
				"sandbox security invariants violated", map[string]string{
					"violations": detail,
					"canary":     probeSummary(*probe),
				})
			s.collect(ctx, acts, req, "")
			return fmt.Errorf("sandbox security invariants violated: %s", detail)
		}
		s.setCondition(ctx, ConditionEgressVerified, ConditionTrue, "Probed", probeSummary(*probe))
	} else {
		s.setCondition(ctx, ConditionEgressVerified, ConditionUnknown, "ProbeDisabled",
			"hardening.egress_probe is disabled: the applied network policy was not measured")
		if lockdown.ProbeEvidenceError != "" {
			s.collectionFailed = true
			s.status.Error = "egress probe evidence persistence failed: " + lockdown.ProbeEvidenceError
		}
	}

	// ── 6. Run the coding agent ─────────────────────────────────────────────
	s.status.CurrentStep = "agent.run"
	var agentOut RunAgentOutput
	agentOpts := agentRetry()
	agentOpts.StartToCloseTimeout = req.AgentTimeout + time.Minute
	agentModel := req.AgentModel
	if s.resources.ModelID != "" {
		agentModel = s.resources.ModelID
	}
	agentErr := executeActivity(ctx, acts.RunAgent, agentOpts, RunAgentInput{
		RunID:          req.RunID,
		SandboxID:      s.sandboxID,
		Harness:        req.AgentHarness,
		Model:          agentModel,
		ModelAPIKeyEnv: s.resources.ModelAPIKeyEnv,
		Prompt:         req.Task,
		RepositoryDir:  req.EffectiveRepositoryDir(),
		Timeout:        req.AgentTimeout,
	}).Get(ctx, &agentOut)

	if agentErr != nil {
		s.agentRan = true
		// The activity exhausted its retries. A cancelled workflow is reported
		// as CANCELLED rather than as an agent failure.
		if isCancellation(agentErr) {
			s.agent.ExitCode = -1
			s.status.AgentOutcome = OutcomeCancelled
			s.agentOutcome = OutcomeCancelled
			s.status.State = StateCancelled
			s.setCondition(ctx, ConditionAgentCompleted, ConditionFalse, "Cancelled", agentErr.Error())
			return fmt.Errorf("run cancelled during agent execution: %w", agentErr)
		}
		s.agent.ExitCode = -1
		s.status.AgentOutcome = OutcomeError
		s.status.State = StateAgentFailed
		s.setCondition(ctx, ConditionAgentCompleted, ConditionFalse, "ActivityFailed", agentErr.Error())
		// An activity can fail after the agent changed the tree. Audit that
		// partial work before recording an ordinary agent failure.
		if err := s.auditAndScan(ctx, acts, req); err != nil {
			return err
		}
		if err := s.quarantineIfTripped(ctx, acts, req, -1); err != nil {
			return err
		}
		return fmt.Errorf("agent execution failed: %w", agentErr)
	}

	if agentOut.EvidenceError != "" {
		s.collectionFailed = true
		s.status.Error = "agent evidence persistence failed: " + agentOut.EvidenceError
	}
	s.agent = agentOut.Result
	s.agentRan = true
	s.agentAttmpt = agentOut.Attempt
	s.blocked = agentOut.Blocked
	s.blockedReason = agentOut.BlockedReason
	s.behavior = agentOut.Behavior
	s.behaviorError = agentOut.BehaviorError
	switch {
	case agentOut.Result.Succeeded():
		s.agentOutcome = OutcomeSuccess
	case agentOut.Result.TimedOut:
		s.agentOutcome = OutcomeTimedOut
	default:
		s.agentOutcome = OutcomeFailed
	}
	s.status.AgentOutcome = s.agentOutcome

	// ── 6a. Behavior quarantine ─────────────────────────────────────────────
	// A tripped monitor stops the run BEFORE verification and before any
	// success can be recorded. The patch is still collected as evidence, but it
	// is never presented as a deliverable that passed its gates.
	//
	// The scan covers the agent's output AND the collected patch. The decision
	// is made once, after both, so a rule that matches the patch (a rewritten
	// build script, for example) cannot be missed by scanning only stdout.

	// ── 6b. The agent's documented stop signal ──────────────────────────────
	// BLOCKED is a legitimate outcome, not a failure. It exists so a stuck
	// agent has an honest exit instead of improvising around its sandbox.
	//
	// Gate integrity is checked BEFORE the stop signal is honoured. Otherwise an
	// agent that rewrote a gate could end the run as BLOCKED — a documented
	// "not a security event" — and never reach GATE_TAMPERED.
	if agentOut.Blocked {
		if err := s.auditAndScan(ctx, acts, req); err != nil {
			return err
		}
		if err := s.quarantineIfTripped(ctx, acts, req, agentOut.Result.ExitCode); err != nil {
			return err
		}
		s.status.VerifyOutcome = OutcomeSkipped
		s.status.State = StateBlocked
		s.setCondition(ctx, ConditionAgentBlocked, ConditionTrue, "AgentReportedBlocked", s.blockedReason)
		s.setCondition(ctx, ConditionAgentCompleted, ConditionFalse, "Blocked", s.blockedReason)
		if s.resources.Hardening.AlertOnBlocked {
			s.recordAlert(ctx, acts, req, AlertBlocked, notify.SeverityWarning,
				"agent reported BLOCKED", map[string]string{"reason": s.blockedReason})
		}
		return fmt.Errorf("agent reported blocked: %s", s.blockedReason)
	}
	s.setCondition(ctx, ConditionAgentBlocked, ConditionFalse, "NotBlocked", agentOut.BlockedSignalError)

	if !agentOut.Result.Succeeded() {
		// There is nothing meaningful to verify when the agent did not
		// succeed. Verification is recorded as SKIPPED, never silently absent.
		// A partial patch is still collected and audited: a failed agent that
		// rewrote its gates is still a gate violation.
		if err := s.auditAndScan(ctx, acts, req); err != nil {
			return err
		}
		if err := s.quarantineIfTripped(ctx, acts, req, agentOut.Result.ExitCode); err != nil {
			return err
		}
		s.status.VerifyOutcome = OutcomeSkipped
		s.status.State = StateAgentFailed
		s.setCondition(ctx, ConditionAgentCompleted, ConditionFalse, agentFailureReason(agentOut.Result),
			fmt.Sprintf("exit=%d timed_out=%v", agentOut.Result.ExitCode, agentOut.Result.TimedOut))
		return fmt.Errorf("agent did not succeed: exit code %d, timed out=%v",
			agentOut.Result.ExitCode, agentOut.Result.TimedOut)
	}
	s.setCondition(ctx, ConditionAgentCompleted, ConditionTrue, "ExitZero",
		fmt.Sprintf("attempt=%d duration=%s", agentOut.Attempt, agentOut.Result.Duration))

	// ── 6. Capture the agent's contribution and audit it ────────────────────
	// Collection happens BEFORE verification because a gate can rewrite tracked
	// files (a build step regenerating a lockfile): capturing afterwards would
	// attribute that side effect to the agent.
	if err := s.auditAndScan(ctx, acts, req); err != nil {
		return err
	}
	if err := s.quarantineIfTripped(ctx, acts, req, agentOut.Result.ExitCode); err != nil {
		return err
	}

	// ── 7. Deterministic verification ───────────────────────────────────────
	s.status.CurrentStep = "verify"
	var verified RunVerificationOutput
	if err := executeActivity(ctx, acts.RunVerification, verificationRetry(), RunVerificationInput{
		RunID:         req.RunID,
		SandboxID:     s.sandboxID,
		Timeout:       req.VerificationTimeout,
		Profile:       req.VerificationProfile,
		RepositoryDir: req.EffectiveRepositoryDir(),
	}).Get(ctx, &verified); err != nil {
		s.verifyRan = true
		s.status.VerifyOutcome = OutcomeError
		s.status.State = StateInfrastructureFailed
		s.collect(ctx, acts, req, VerificationPhase)
		return fmt.Errorf("verification could not be executed: %w", err)
	}

	if verified.EvidenceError != "" {
		s.collectionFailed = true
		s.status.Error = "verification evidence persistence failed: " + verified.EvidenceError
	}
	s.verify = verified.Result
	s.verifyRan = true
	if verified.Result.Passed {
		s.status.VerifyOutcome = OutcomeSuccess
	} else {
		s.status.VerifyOutcome = OutcomeFailed
	}

	// ── 8. Did the gates mutate the tree? ───────────────────────────────────
	// Recorded as evidence, never folded into the deliverable.
	s.collect(ctx, acts, req, VerificationPhase)

	// ── 8b. Human review gate ──────────────────────────────────────────────
	// The gate runs AFTER the deliverable patch is captured and the gates have
	// reported, so a human reviews the same artifacts the record contains. It
	// pauses the run, optionally checkpoints the sandbox and publishes preview
	// URLs, and waits for a decision or a timeout. When no review is
	// configured this is a no-op, which keeps the MVP path unchanged.
	if s.resources.Review {
		if err := s.reviewGate(ctx, acts, req); err != nil {
			return err
		}
	}

	// ── 8c. Record the deterministic gate outcome ──────────────────────────
	if verified.Result.Passed {
		s.setCondition(ctx, ConditionVerified, ConditionTrue, "AllGatesPassed", "")
	} else {
		s.setCondition(ctx, ConditionVerified, ConditionFalse, "GateFailed",
			strings.Join(verified.Result.FailingSteps(), ", "))
	}

	// ── 8d. Budget check ───────────────────────────────────────────────────
	// A budget is evidence, not enforcement: the factory never rewrites a
	// result because a ceiling was crossed. The recorded usage is exactly what
	// the harness reported, and a missing report contributes zero.
	costUSD, tokens := usageOf(s.agent)
	s.budgetExceeded = s.resources.BudgetConfig.Exceeded(
		costUSD, tokens, workflow.Now(ctx).Sub(s.status.StartedAt),
	)
	if len(s.budgetExceeded) > 0 {
		s.setCondition(ctx, ConditionBudgetExceeded, ConditionTrue, "CeilingCrossed",
			strings.Join(s.budgetExceeded, "; "))
	} else {
		s.setCondition(ctx, ConditionBudgetExceeded, ConditionFalse, "WithinBudget", "")
	}

	// ── 9. Decide the factory result ────────────────────────────────────────
	// Deterministic exit codes decide whether the CHANGE IS GOOD. A human
	// decision decides whether it is ACCEPTED. When review is configured the two
	// are separate outcomes: a rejection means the gates passed but a person
	// refused the change, which must never be reported as SUCCEEDED. Recording
	// it as SUCCEEDED would make the human gate decorative.
	if !verified.Result.Passed {
		s.status.State = StateVerificationFailed
		return fmt.Errorf("verification failed: gates [%s] did not pass",
			strings.Join(verified.Result.FailingSteps(), ", "))
	}
	if s.reviewRequested && s.reviewOutcome == HumanResultRejected {
		s.status.State = StateRejected
		return fmt.Errorf("change rejected by human review: %s", s.reviewNote)
	}
	s.status.State = StateSucceeded
	return nil
}

// auditGates collects the agent's contribution and checks gate integrity.
//
// It returns true when a gate was modified. Every exit path that captures a
// patch goes through here, so "the agent failed" or "the agent reported
// blocked" can never hide a rewritten gate.
func (s *workflowState) auditGates(ctx workflow.Context, acts *Activities, req RunRequest) (bool, error) {
	s.collect(ctx, acts, req, "")

	var integrity CheckGateIntegrityOutput
	if err := executeActivity(ctx, acts.CheckGateIntegrity, noRetry(), CheckGateIntegrityInput{
		RunID:         req.RunID,
		SandboxID:     s.sandboxID,
		RepositoryDir: req.EffectiveRepositoryDir(),
		BaselineSHA:   s.baseline.SHA,
		GatePaths:     s.resources.Hardening.GatePaths,
		Allowed:       s.resources.Hardening.AllowGateSelfModification,
	}).Get(ctx, &integrity); err != nil {
		// A check that could not run is not a clean gate.
		s.status.State = StateInfrastructureFailed
		s.setCondition(ctx, ConditionGateIntegrity, ConditionFalse, "CheckFailed", err.Error())
		return false, fmt.Errorf("check gate integrity: %w", err)
	}
	s.gateIntegrity = &integrity.Integrity
	if integrity.EvidenceError != "" {
		s.collectionFailed = true
		s.status.Error = "gate integrity evidence persistence failed: " + integrity.EvidenceError
	}
	if len(integrity.Integrity.Modified) > 0 {
		detail := strings.Join(integrity.Integrity.Modified, ", ")
		s.status.VerifyOutcome = OutcomeSkipped
		s.status.State = StateGateTampered
		s.setCondition(ctx, ConditionGateIntegrity, ConditionFalse, "GateModified", detail)
		s.recordAlert(ctx, acts, req, AlertGateTampered, notify.SeverityCritical,
			"agent modified the gates that judge it", map[string]string{"paths": detail})
		return true, nil
	}
	if !integrity.Integrity.Checked {
		// Never report an unperformed check as clean.
		s.setCondition(ctx, ConditionGateIntegrity, ConditionUnknown, "NotChecked",
			"gate integrity was not checked")
		return false, nil
	}
	s.setCondition(ctx, ConditionGateIntegrity, ConditionTrue, "Unmodified",
		fmt.Sprintf("method=%s gates=%d", integrity.Integrity.Method, len(integrity.Integrity.GatePaths)))
	return false, nil
}

// auditAndScan collects the patch, checks gate integrity, and scans the patch.
//
// It is the single audit step every exit path runs, so no agent outcome can
// skip it. A gate violation or an infrastructure failure ends the run here.
func (s *workflowState) auditAndScan(ctx workflow.Context, acts *Activities, req RunRequest) error {
	tampered, err := s.auditGates(ctx, acts, req)
	if err != nil {
		return err
	}
	if tampered {
		return fmt.Errorf("gate integrity violated: the agent modified %s",
			strings.Join(s.gateIntegrity.Modified, ", "))
	}
	if !s.resources.Hardening.BehaviorMonitorEnabled {
		return nil
	}
	var out ScanPatchOutput
	if err := executeActivity(ctx, acts.ScanPatch, noRetry(), ScanPatchInput{
		RunID:                     req.RunID,
		AllowGateSelfModification: s.resources.Hardening.AllowGateSelfModification,
	}).Get(ctx, &out); err != nil {
		// A scan that could not run is recorded, never silently skipped: an
		// unscanned patch must not look like a clean one.
		s.behaviorError = appendError(s.behaviorError, "patch scan: "+err.Error())
		return nil
	}
	s.behavior = mergeThreatReports(s.behavior, out.Report)
	if out.Error != "" {
		s.behaviorError = appendError(s.behaviorError, out.Error)
	}
	return nil
}

// quarantineIfTripped stops the run when the behavior monitor tripped, and
// records the scan outcome otherwise.
//
// It runs after every scan has contributed, so the verdict covers the agent's
// output and the collected patch together.
func (s *workflowState) quarantineIfTripped(ctx workflow.Context, acts *Activities, req RunRequest, exitCode int) error {
	if s.behavior == nil {
		reason := "MonitorDisabled"
		message := "hardening.behavior_monitor is disabled: agent output was not scanned"
		if s.behaviorError != "" {
			reason = "ScanError"
			message = s.behaviorError
		}
		s.setCondition(ctx, ConditionBehaviorScanned, ConditionUnknown, reason, message)
		return nil
	}
	if s.behavior.Tripped {
		s.status.VerifyOutcome = OutcomeSkipped
		s.status.State = StateQuarantined
		s.setCondition(ctx, ConditionBehaviorScanned, ConditionFalse, "Tripped", threatReportSummary(*s.behavior))
		s.setCondition(ctx, ConditionAgentCompleted, ConditionFalse, "Quarantined",
			fmt.Sprintf("exit=%d findings=%d", exitCode, len(s.behavior.Findings)))
		s.recordAlert(ctx, acts, req, AlertQuarantine, notify.SeverityCritical,
			"agent behavior monitor tripped", threatReportDetail(*s.behavior))
		return fmt.Errorf("run quarantined by behavior monitor: %s", threatReportSummary(*s.behavior))
	}
	reason := "Clean"
	if len(s.behavior.Findings) > 0 {
		reason = "FindingsBelowThreshold"
	}
	message := threatReportSummary(*s.behavior)
	if s.behaviorError != "" {
		message = appendError(message, s.behaviorError)
	}
	s.setCondition(ctx, ConditionBehaviorScanned, ConditionTrue, reason, message)
	return nil
}

// recordAlert delivers one security alert through an activity.
//
// Alerting is deliberately outside the run's success criteria: a webhook outage
// must not become a factory outage. The record, delivered or not, is carried
// into the manifest so a silent alerting failure stays visible.
func (s *workflowState) recordAlert(
	ctx workflow.Context,
	acts *Activities,
	req RunRequest,
	kind string,
	severity notify.Severity,
	summary string,
	detail map[string]string,
) {
	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	var out AlertOutput
	if err := executeActivity(ctx, acts.RaiseAlert, opts, AlertInput{
		RunID:    req.RunID,
		Kind:     kind,
		Severity: string(severity),
		Summary:  summary,
		Detail:   detail,
	}).Get(ctx, &out); err != nil {
		s.alerts = append(s.alerts, AlertRecord{
			Kind:     kind,
			Severity: string(severity),
			Summary:  summary,
			Error:    err.Error(),
		})
		return
	}
	s.alerts = append(s.alerts, out.Record)
}

// reviewGate pauses a run for a human decision.
//
// The order inside the gate matters: previews are published before the sandbox
// is suspended, and the sandbox is resumed before anything else touches it. A
// suspend that succeeds followed by a failed resume is the one combination
// that must fail the run, because every later activity needs a live sandbox.
func (s *workflowState) reviewGate(ctx workflow.Context, acts *Activities, req RunRequest) error {
	s.status.CurrentStep = "review.pause"
	s.status.State = StatePaused
	s.reviewRequested = true
	s.setCondition(ctx, ConditionReviewPaused, ConditionTrue, "AwaitingHuman",
		"timeout="+s.resources.ReviewTimeout.Std().String())

	// 1. Publish previews while the sandbox is definitely awake.
	//
	// Publication is an INBOUND exposure of the sandbox, so it is gated on the
	// same operator switch as `factory preview`. The review gate previously
	// published regardless of [sandbox] allow_preview, which made the
	// documented default-closed posture untrue for the review path.
	if len(s.reviewPorts) > 0 && !s.resources.AllowPreview {
		s.previewOutcome = OutcomeSkipped
		s.reviewError = appendError(s.reviewError, "preview: skipped because [sandbox] allow_preview is not set")
	} else if len(s.reviewPorts) > 0 {
		var previews PreviewOutput
		if err := executeActivity(ctx, acts.PreviewSandbox, infraRetry(), PreviewInput{
			RunID:     req.RunID,
			SandboxID: s.sandboxID,
			Ports:     s.reviewPorts,
		}).Get(ctx, &previews); err != nil {
			// A preview is a convenience for the reviewer; failing to publish one
			// must not fail the run. The reason is kept for the log and evidence.
			s.previewOutcome = OutcomeFailed
			s.reviewError = appendError(s.reviewError, "preview: "+err.Error())
		} else {
			s.previewOutcome = previews.Outcome
			s.previews = previews.Links
			for _, failure := range previews.Failures {
				s.reviewError = appendError(s.reviewError, "preview: "+failure)
			}
		}
	}

	// 2. Checkpoint the sandbox when the operator asked for it.
	if s.resources.Suspend {
		var suspended SandboxLifecycleOutput
		if err := executeActivity(ctx, acts.SuspendSandbox, infraRetry(), SandboxLifecycleInput{
			RunID:     req.RunID,
			SandboxID: s.sandboxID,
			Reason:    "review gate",
		}).Get(ctx, &suspended); err != nil {
			// Exhausted retries: the run continues with a live sandbox. Reporting
			// the failure is what keeps the pause honest.
			s.suspendOutcome = OutcomeFailed
			s.suspendError = appendError(s.suspendError, err.Error())
		} else {
			s.suspendOutcome = suspended.Outcome
			if suspended.Error != "" {
				s.suspendError = appendError(s.suspendError, suspended.Error)
			}
		}
	} else {
		s.suspendOutcome = OutcomeSkipped
		s.suspendError = appendError(s.suspendError, "review.suspend is disabled")
	}

	// 3. Wait for a decision or the timeout. The wait is a Temporal timer, so
	// it costs nothing while it elapses and survives worker restarts.
	decision, timedOut := waitForReviewDecision(ctx, s.resources.ReviewTimeout.Std())
	switch {
	case timedOut:
		s.reviewOutcome = HumanResultTimeout
		s.status.CurrentStep = "review.timeout"
	case decision.Decision == HumanResultApproved:
		s.reviewOutcome = HumanResultApproved
		s.reviewNote = decision.Note
		s.status.CurrentStep = "review.approved"
	default:
		s.reviewOutcome = HumanResultRejected
		s.reviewNote = decision.Note
		s.status.CurrentStep = "review.rejected"
	}
	s.setCondition(ctx, ConditionReviewPaused, ConditionFalse, "DecisionReceived",
		"outcome="+s.reviewOutcome)

	// 4. Wake the sandbox before anything else uses it.
	if s.suspendOutcome == OutcomeSuccess {
		var resumed SandboxLifecycleOutput
		if err := executeActivity(ctx, acts.ResumeSandbox, infraRetry(), SandboxLifecycleInput{
			RunID:     req.RunID,
			SandboxID: s.sandboxID,
			Reason:    "review gate complete",
		}).Get(ctx, &resumed); err != nil {
			s.resumeFailed = true
			s.suspendError = appendError(s.suspendError, "resume: "+err.Error())
			s.setCondition(ctx, ConditionReviewPaused, ConditionFalse, "ResumeFailed", err.Error())
			s.status.State = StateInfrastructureFailed
			return fmt.Errorf("resume sandbox after review: %w", err)
		}
		if resumed.Outcome != OutcomeSuccess {
			s.resumeFailed = true
			reason := resumed.Error
			if reason == "" {
				reason = "resume did not report success"
			}
			s.suspendError = appendError(s.suspendError, "resume: "+reason)
			s.setCondition(ctx, ConditionReviewPaused, ConditionFalse, "ResumeFailed", reason)
			s.status.State = StateInfrastructureFailed
			return fmt.Errorf("resume sandbox after review: %s (%s)", reason, resumed.Outcome)
		}
	}

	s.status.State = StateRunning
	return nil
}

// waitForReviewDecision blocks until a decision arrives or the timeout expires.
//
// A timeout is not a failure: it means nobody reviewed the change, and the
// factory proceeds to finish normally so a forgotten run cannot hold a sandbox
// forever. The timeout is recorded as the review outcome so "no decision" is
// visible rather than implied by absence.
func waitForReviewDecision(ctx workflow.Context, timeout time.Duration) (ReviewDecision, bool) {
	if timeout <= 0 {
		timeout = DefaultReviewTimeout
	}
	signal := workflow.GetSignalChannel(ctx, ReviewSignalName)
	timer := workflow.NewTimer(ctx, timeout)

	var decision ReviewDecision
	timedOut := false
	workflow.NewSelector(ctx).
		AddReceive(signal, func(ch workflow.ReceiveChannel, _ bool) {
			ch.Receive(ctx, &decision)
		}).
		AddFuture(timer, func(workflow.Future) {
			timedOut = true
		}).
		Select(ctx)
	return decision, timedOut
}

// agentFailureReason classifies agent failure for a condition reason.
func agentFailureReason(result agentharness.Result) string {
	if result.TimedOut {
		return "TimedOut"
	}
	return "NonZeroExit"
}

// usageOf extracts recorded usage from an agent result.
//
// A harness that reports nothing contributes zero. The alternative — guessing
// from tokens or duration — would make budget evidence fiction.
func usageOf(result agentharness.Result) (costUSD float64, tokens int64) {
	if result.CostUSD != nil {
		costUSD = *result.CostUSD
	}
	if result.TokensIn != nil {
		tokens += *result.TokensIn
	}
	if result.TokensOut != nil {
		tokens += *result.TokensOut
	}
	return costUSD, tokens
}

// appendError joins messages without duplicating an empty prefix.
func appendError(existing, add string) string {
	if existing == "" {
		return add
	}
	return existing + "; " + add
}

// boolString renders a boolean for a condition message.
func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// intString renders an int for a condition message.
func intString(v int) string { return strconv.Itoa(v) }

// collect extracts the patch and post-run repository state.
//
// The empty phase captures the AGENT's contribution and is called BEFORE
// verification, so a gate that rewrites a tracked file cannot be mistaken for
// part of the agent's change. The verification phase captures the tree
// afterwards purely to report such drift.
//
// It runs on failure paths too, so a failed run's evidence is as complete as a
// successful one's.
func (s *workflowState) collect(ctx workflow.Context, acts *Activities, req RunRequest, phase string) {
	if phase == "" {
		s.status.CurrentStep = "artifacts.collect"
	} else {
		s.status.CurrentStep = "artifacts.verify-drift"
	}
	var collected CollectArtifactsOutput
	if err := executeActivity(ctx, acts.CollectArtifacts, infraRetry(), CollectArtifactsInput{
		RunID:         req.RunID,
		SandboxID:     s.sandboxID,
		RepositoryDir: req.EffectiveRepositoryDir(),
		BaselineSHA:   s.baseline.SHA,
		Phase:         phase,
	}).Get(ctx, &collected); err != nil {
		s.collectionFailed = true
		// Artifact collection failure must not erase the run's verdict, but the
		// gap is recorded so it is visible.
		if s.status.Error == "" {
			s.status.Error = "artifact collection failed: " + err.Error()
		}
		return
	}
	if phase == VerificationPhase {
		s.postPatchPath = collected.PatchArtifact
		// Drift means the tree changed AFTER the agent's patch was captured.
		// The post-verification diff is non-empty whenever the agent changed
		// anything, so comparing it to the agent's own patch is the only
		// correct test. The comparison is a pure string operation, so it is
		// safe in workflow code.
		s.verificationDrift = collected.Patch != s.patch
		s.setCondition(ctx, ConditionArtifactsCollected, ConditionTrue, "PostVerification",
			"drift="+boolString(s.verificationDrift))
		return
	}
	s.patch = collected.Patch
	s.patchPath = collected.PatchArtifact
	s.resultingSHA = collected.ResultingSHA
	s.setCondition(ctx, ConditionArtifactsCollected, ConditionTrue, "PatchCaptured",
		"bytes="+intString(len(collected.Patch)))
}

// cleanupSandbox destroys the sandbox on every exit path.
//
// A disconnected context is used so cleanup still runs when the workflow has
// been cancelled — the case in which a leaked VM is most likely.
func cleanupSandbox(ctx workflow.Context, acts *Activities, state *workflowState) {
	// Idempotent: the explicit call and the deferred safety net must not both
	// destroy (and both report) the same sandbox.
	if state.cleanupDone {
		return
	}
	state.cleanupDone = true

	if !state.createAttempted {
		// Nothing was created, so there is nothing to clean up. That is a
		// success, not an omission.
		state.cleanup = CleanupResult{
			Attempted:  false,
			Outcome:    OutcomeSkipped,
			FinishedAt: workflow.Now(ctx),
		}
		return
	}

	disconnected, _ := workflow.NewDisconnectedContext(ctx)
	actx := workflow.WithActivityOptions(disconnected, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		// Cleanup is retried hard: a leaked VM is expensive and Destroy is
		// idempotent.
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    60 * time.Second,
			MaximumAttempts:    8,
		},
	})

	var cleanup CleanupResult
	if err := workflow.ExecuteActivity(actx, acts.DestroySandbox, DestroySandboxInput{
		ExecutionID: state.executionID,
		RunID:       state.status.RunID,
		SandboxID:   state.sandboxID,
	}).Get(disconnected, &cleanup); err != nil {
		cleanup = CleanupResult{
			Attempted:  true,
			Outcome:    OutcomeError,
			SandboxID:  state.sandboxID,
			Error:      err.Error(),
			FinishedAt: workflow.Now(ctx),
		}
		workflow.GetLogger(ctx).Error("sandbox cleanup failed; a sandbox may have leaked",
			"run_id", state.status.RunID, "sandbox_id", state.sandboxID, "error", err)
	}
	state.cleanup = cleanup
	state.status.SandboxGone = cleanup.Verified
}

// finalizeRun writes the durable manifest.
//
// It also uses a disconnected context so the record survives cancellation: a
// cancelled run that leaves no trace is not auditable.
func finalizeRun(
	ctx workflow.Context,
	acts *Activities,
	state *workflowState,
	req RunRequest,
	info *workflow.Info,
	startedAt time.Time,
) (RunManifest, error) {
	disconnected, _ := workflow.NewDisconnectedContext(ctx)
	actx := workflow.WithActivityOptions(disconnected, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    5,
		},
	})

	agentOutcome := OutcomeSkipped
	if state.agentRan {
		agentOutcome = state.agentOutcome
		if agentOutcome == "" {
			agentOutcome = OutcomeError
		}
	}
	verifyOutcome := OutcomeSkipped
	if state.verifyRan {
		verifyOutcome = OutcomeFailed
		if state.verify.Passed {
			verifyOutcome = OutcomeSuccess
		}
	}
	// A verification stage that was skipped because the agent failed keeps the
	// SKIPPED outcome that run() already recorded.
	if state.status.VerifyOutcome == OutcomeError {
		verifyOutcome = OutcomeError
	}

	input := FinalizeInput{
		Request:                 req,
		Validate:                state.validate,
		Intake:                  state.intake,
		SandboxID:               state.sandboxID,
		SandboxTemplate:         state.template,
		HarnessVersion:          state.harnessVer,
		AgentResult:             state.agent,
		AgentOutcome:            agentOutcome,
		AgentAttempt:            state.agentAttmpt,
		VerificationResult:      state.verify,
		VerificationOutcome:     verifyOutcome,
		Baseline:                state.baseline,
		Patch:                   state.patch,
		PatchArtifact:           state.patchPath,
		ResultingSHA:            state.resultingSHA,
		VerificationMutatedTree: state.verificationDrift,
		PostVerificationPatch:   state.postPatchPath,
		FactoryResult:           state.status.State,
		Cleanup:                 state.cleanup,
		WorkflowID:              info.WorkflowExecution.ID,
		WorkflowRunID:           info.WorkflowExecution.RunID,
		StartedAt:               startedAt,
		CompletedAt:             workflow.Now(ctx),
		Error:                   state.status.Error,
		Conditions:              state.status.Conditions,
		Resources:               state.resources,
		SuspendResult:           state.suspendOutcome,
		SuspendError:            state.suspendError,
		PreviewResult:           state.previewOutcome,
		ReviewConfigured:        state.reviewRequested,
		ReviewOutcome:           state.reviewOutcome,
		ReviewNote:              state.reviewNote,
		ReviewError:             state.reviewError,
		Previews:                state.previews,
		BudgetExceeded:          state.budgetExceeded,
		Hardening: &HardeningEvidence{
			EgressProbe:   state.egressProbe,
			Behavior:      state.behavior,
			BehaviorError: state.behaviorError,
			GateIntegrity: state.gateIntegrity,
			Alerts:        state.alerts,
		},
	}

	var out FinalizeOutput
	if err := workflow.ExecuteActivity(actx, acts.FinalizeResult, input).Get(disconnected, &out); err != nil {
		return RunManifest{}, err
	}
	return out.Manifest, nil
}

// ── activity options ──────────────────────────────────────────────────────

// noRetry disables retries for deterministic operations.
func noRetry() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
}

// infraRetry retries infrastructure operations that are safe to repeat.
func infraRetry() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    5,
		},
	}
}

// agentRetry does not retry agent execution: transport failure can leave a
// completed agent whose acknowledgement was lost. Re-execution is unsafe.
//
// The activity still records its Temporal attempt number for diagnosis.
func agentRetry() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Minute,
		// A heartbeat keeps long agent runs from being declared dead while the
		// sandbox is still working.
		HeartbeatTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    1,
		},
	}
}

// verificationRetry retries verification. It is deterministic, so retrying can
// only help.
func verificationRetry() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
}

// executeActivity keeps activity invocation consistent across the workflow.
func executeActivity(ctx workflow.Context, activity any, opts workflow.ActivityOptions, args ...any) workflow.Future {
	return workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), activity, args...)
}

// isCancellation reports whether an activity error is a workflow cancellation.
func isCancellation(err error) bool {
	if err == nil {
		return false
	}
	if temporal.IsCanceledError(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "canceled") || strings.Contains(msg, "cancelled")
}
