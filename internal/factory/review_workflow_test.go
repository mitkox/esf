package factory

import (
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

// These tests exercise the AX-inspired review gate: a run pauses after the
// deterministic gates report, optionally checkpoints its sandbox, publishes
// preview URLs, waits for a human decision or a timeout, and then resumes and
// finishes down the normal path.
//
// The properties that matter are the ones an operator would be misled by if
// they were wrong:
//   - the run is actually paused and the sandbox is actually checkpointed;
//   - a timeout is recorded as "timeout", not implied by an empty field;
//   - a provider without suspend capability is recorded as SKIPPED, so "free
//     while paused" is never claimed when it was not true;
//   - the dormant sandbox is always woken before the run continues;
//   - a human rejection does not falsify the deterministic gate result.

// reviewConfig returns a review gate configuration with a given timeout.
func reviewConfig(timeout time.Duration) ReviewConfig {
	return ReviewConfig{
		Enabled:      true,
		Timeout:      tomlx.FromStd(timeout),
		PreviewPorts: []int{3000},
		Suspend:      true,
	}
}

// TestWorkflowReviewGateApprovedPausesAndResumes proves the happy review path.
func TestWorkflowReviewGateApprovedPausesAndResumes(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))

	envSetup := func(env *testsuite.TestWorkflowEnvironment) {
		// A reviewer approves two seconds into the pause.
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(ReviewSignalName, ReviewDecision{
				Decision: HumanResultApproved,
				Actor:    "reviewer@example.com",
			})
		}, 2*time.Second)
	}

	manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{
		mutateConfig: func(cfg *Config) { cfg.Review = reviewConfig(time.Hour) },
	}, envSetup)
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}

	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s, want SUCCEEDED (error: %s)", manifest.FactoryResult, manifest.Error)
	}
	if !manifest.ReviewConfigured {
		t.Fatal("review_configured = false, want true")
	}
	if manifest.ReviewOutcome != HumanResultApproved {
		t.Fatalf("review_outcome = %q, want %q", manifest.ReviewOutcome, HumanResultApproved)
	}
	if manifest.HumanResult != HumanResultApproved {
		t.Fatalf("human_result = %q, want %q", manifest.HumanResult, HumanResultApproved)
	}
	if manifest.SuspendResult != OutcomeSuccess {
		t.Fatalf("suspend_result = %s, want SUCCESS (error: %s)", manifest.SuspendResult, manifest.SuspendError)
	}
	if len(manifest.Previews) != 1 || manifest.Previews[0].Port != 3000 {
		t.Fatalf("previews = %+v, want one link for port 3000", manifest.Previews)
	}
	if len(fake.SuspendCalls()) != 1 {
		t.Fatalf("suspend calls = %v, want exactly one checkpoint", fake.SuspendCalls())
	}
	if len(fake.ResumeCalls()) != 1 {
		t.Fatalf("resume calls = %v, want exactly one wake", fake.ResumeCalls())
	}
	// The sandbox must be awake when the run finishes cleaning up.
	if len(fake.Live()) != 0 {
		t.Fatalf("sandbox leaked: %v", fake.Live())
	}

	// The pause and the decision are both visible in the conditions.
	assertCondition(t, manifest.Conditions, ConditionReviewPaused, ConditionFalse, "DecisionReceived")
}

// TestWorkflowReviewGateTimeoutIsRecorded proves a forgotten gate cannot hold a
// sandbox forever and that "nobody decided" is distinguishable from "approved".
func TestWorkflowReviewGateTimeoutIsRecorded(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))

	manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{
		mutateConfig: func(cfg *Config) { cfg.Review = reviewConfig(30 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}

	if manifest.ReviewOutcome != HumanResultTimeout {
		t.Fatalf("review_outcome = %q, want %q", manifest.ReviewOutcome, HumanResultTimeout)
	}
	if manifest.HumanResult != HumanResultTimeout {
		t.Fatalf("human_result = %q, want %q", manifest.HumanResult, HumanResultTimeout)
	}
	// The run still finishes normally: a timeout is not a failure.
	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s, want SUCCEEDED", manifest.FactoryResult)
	}
	// The checkpoint was taken and released even without a decision.
	if len(fake.ResumeCalls()) != 1 {
		t.Fatalf("resume calls = %v, want the sandbox woken after the timeout", fake.ResumeCalls())
	}
}

// TestBudgetWallClockDoesNotCancelRun proves a budget is post-run evidence,
// not a second execution timeout. The human deliberately answers after the
// budget ceiling; deterministic gates still decide the factory result.
func TestBudgetWallClockDoesNotCancelRun(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))

	manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{
		mutateConfig: func(cfg *Config) {
			cfg.Review = reviewConfig(time.Hour)
			cfg.Budgets = map[string]BudgetConfig{
				DefaultBudget: {MaxWallClock: tomlx.FromStd(time.Second)},
			}
		},
	}, func(env *testsuite.TestWorkflowEnvironment) {
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(ReviewSignalName, ReviewDecision{Decision: HumanResultApproved})
		}, 2*time.Second)
	})
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s, want SUCCEEDED; budget must not enforce", manifest.FactoryResult)
	}
	if len(manifest.BudgetExceeded) != 1 || !strings.Contains(manifest.BudgetExceeded[0], "wall clock") {
		t.Fatalf("budget_exceeded = %v, want wall-clock evidence", manifest.BudgetExceeded)
	}
	cond, ok := ConditionFor(manifest.Conditions, ConditionBudgetExceeded)
	if !ok || cond.Status != ConditionTrue {
		t.Fatalf("budget condition = %+v, want BudgetExceeded=True", cond)
	}
}

// TestWorkflowReviewGateRejectionDoesNotFalsifyGates proves the separation: the
// gates passed, so the factory result stays SUCCEEDED, while the human decision
// is recorded as REJECTED and the change returns to OPEN.
func TestWorkflowReviewGateRejectionDoesNotFalsifyGates(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))

	manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{
		mutateConfig: func(cfg *Config) { cfg.Review = reviewConfig(time.Hour) },
	}, func(env *testsuite.TestWorkflowEnvironment) {
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(ReviewSignalName, ReviewDecision{
				Decision: HumanResultRejected,
				Note:     "the greeting should be formal",
			})
		}, time.Second)
	})
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}

	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s, want SUCCEEDED: a human rejection must not rewrite the gate result", manifest.FactoryResult)
	}
	if manifest.HumanResult != HumanResultRejected {
		t.Fatalf("human_result = %q, want REJECTED", manifest.HumanResult)
	}
	if manifest.ReviewNote != "the greeting should be formal" {
		t.Fatalf("review_note = %q, want the durable rework instruction", manifest.ReviewNote)
	}
	if manifest.VerificationResult != OutcomeSuccess {
		t.Fatalf("verification_result = %s, want SUCCESS: the gate passed and that is a fact", manifest.VerificationResult)
	}
}

// capsOverride hides lifecycle capabilities while keeping the provider's
// methods. It models a deployment whose sandbox runtime cannot checkpoint: the
// factory must report SKIPPED rather than claim a free pause.
type capsOverride struct {
	*sandbox.Fake
	caps sandbox.Capabilities
}

func (c capsOverride) Capabilities() sandbox.Capabilities { return c.caps }

// TestWorkflowReviewGateWithoutSuspendCapabilityIsHonest proves the degraded
// path: the run still pauses for a human, but the manifest says the sandbox was
// not checkpointed instead of implying cost was saved.
func TestWorkflowReviewGateWithoutSuspendCapabilityIsHonest(t *testing.T) {
	base := sandbox.NewFake()
	base.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	provider := capsOverride{Fake: base, caps: sandbox.Capabilities{}}

	manifest, err := runWorkflowOpts(t, provider, baseRequest(t), workflowRunOptions{
		mutateConfig: func(cfg *Config) { cfg.Review = reviewConfig(30 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}

	if manifest.SuspendResult != OutcomeSkipped {
		t.Fatalf("suspend_result = %s, want SKIPPED for a provider without suspend", manifest.SuspendResult)
	}
	if manifest.PreviewResult != OutcomeSkipped {
		t.Fatalf("preview_result = %s, want SKIPPED for a provider without preview", manifest.PreviewResult)
	}
	if !strings.Contains(manifest.ReviewError, "does not support preview") {
		t.Fatalf("review_error = %q, want the preview capability reason", manifest.ReviewError)
	}
	if manifest.SuspendError == "" {
		t.Fatal("suspend_error is empty: a skipped checkpoint must explain itself")
	}
	if len(base.SuspendCalls()) != 0 {
		t.Fatalf("suspend was called on a provider without the capability: %v", base.SuspendCalls())
	}
	if manifest.ReviewOutcome != HumanResultTimeout {
		t.Fatalf("review_outcome = %q, want timeout", manifest.ReviewOutcome)
	}
}

// TestWorkflowReviewGateRequiresSuccessfulResume covers an asymmetric provider:
// it can checkpoint but cannot wake the sandbox. A skipped wake is fatal after
// a successful checkpoint; continuing would leave later work pointed at a
// dormant sandbox.
func TestWorkflowReviewGateRequiresSuccessfulResume(t *testing.T) {
	base := sandbox.NewFake()
	base.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	provider := capsOverride{Fake: base, caps: sandbox.Capabilities{Suspend: true}}

	manifest, err := runWorkflowOpts(t, provider, baseRequest(t), workflowRunOptions{
		mutateConfig: func(cfg *Config) { cfg.Review = reviewConfig(time.Minute) },
	})
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if manifest.FactoryResult != StateInfrastructureFailed {
		t.Fatalf("factory_result = %s, want INFRASTRUCTURE_FAILED", manifest.FactoryResult)
	}
	if !strings.Contains(manifest.SuspendError, "does not support resume") {
		t.Fatalf("suspend_error = %q, want the failed resume reason", manifest.SuspendError)
	}
}

// assertCondition checks one condition's status and reason.
func assertCondition(t *testing.T, conditions []Condition, wantType ConditionType, wantStatus ConditionStatus, wantReason string) {
	t.Helper()
	cond, ok := ConditionFor(conditions, wantType)
	if !ok {
		t.Fatalf("condition %s is missing from %s", wantType, ConditionSummary(conditions))
	}
	if cond.Status != wantStatus {
		t.Fatalf("condition %s status = %s, want %s", wantType, cond.Status, wantStatus)
	}
	if wantReason != "" && !strings.Contains(cond.Reason, wantReason) {
		t.Fatalf("condition %s reason = %q, want it to contain %q", wantType, cond.Reason, wantReason)
	}
}
