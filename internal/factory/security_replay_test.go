package factory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

func configureSecurityFixture(t *testing.T) func(*Config) {
	t.Helper()
	profile := securityProfileFixture(t)
	return func(cfg *Config) {
		cfg.SecurityAssessmentProfiles = map[string]SecurityAssessmentProfileConfig{"tenant-http": profile}
		cfg.Scopes = map[string]ScopeConfig{DefaultScope: {SecurityAssessments: []string{"tenant-http"}}}
	}
}

func TestSecurityWorkflowDisabledAndOldVersionPreservePriorPath(t *testing.T) {
	for _, oldVersion := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "old-version"}[oldVersion], func(t *testing.T) {
			fake := sandbox.NewFake()
			fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
			req := baseRequest(t)
			if oldVersion {
				req.SecurityAssessmentProfile = "tenant-http"
			}
			manifest, err := runWorkflowOpts(t, fake, req, workflowRunOptions{mutateConfig: configureSecurityFixture(t)}, func(env *testsuite.TestWorkflowEnvironment) {
				if oldVersion {
					env.OnGetVersion(securityAssessmentVersion, workflow.DefaultVersion, workflow.Version(1)).Return(workflow.DefaultVersion).Once()
				}
				// Any new assessment activity in the preserved path is a failure.
				env.OnActivity("CaptureSecurityCandidate", mock.Anything, mock.Anything).Return(func(context.Context, CaptureSecurityCandidateInput) (SecurityCandidateReference, error) {
					t.Error("preserved workflow path captured a security candidate")
					return SecurityCandidateReference{}, nil
				})
				env.OnActivity("AssessSecurity", mock.Anything, mock.Anything).Return(func(context.Context, SecurityAssessmentInput) (SecurityAssessmentOutput, error) {
					t.Error("preserved workflow path ran a security assessment")
					return SecurityAssessmentOutput{}, nil
				})
			})
			if err != nil || manifest.FactoryResult != StateSucceeded || manifest.SecurityAssessment != nil {
				t.Fatalf("prior path changed: result=%+v err=%v", manifest, err)
			}
		})
	}
}

func TestSecurityWorkflowEnabledMissingExecutorRemainsAdvisory(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	req := baseRequest(t)
	req.SecurityAssessmentProfile = "tenant-http"
	manifest, err := runWorkflowOpts(t, fake, req, workflowRunOptions{mutateConfig: configureSecurityFixture(t)})
	if err != nil || manifest.FactoryResult != StateSucceeded {
		t.Fatalf("missing advisory executor altered functional result: %s %v", manifest.FactoryResult, err)
	}
	if manifest.SecurityAssessment == nil || manifest.SecurityAssessment.Status != "incomplete" || manifest.SecurityAssessment.ErrorCode != "executor_unavailable" {
		t.Fatalf("missing executor was not explicit: %+v", manifest.SecurityAssessment)
	}
	if manifest.HumanResult == HumanResultApproved || len(fake.Live()) != 0 {
		t.Fatalf("advice approved the change or leaked builder: %+v", manifest)
	}
}

func TestSecurityWorkflowMandatoryFailureStillGates(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(func(cmd sandbox.Command) (sandbox.Execution, error) {
		if strings.Contains(strings.Join(cmd.Argv, " "), "./build.sh") {
			return sandbox.Execution{ExitCode: 1, Stdout: "mandatory gate failed"}, nil
		}
		return gitAwareExecute("diff --git a/greeting.py b/greeting.py")(cmd)
	})
	req := baseRequest(t)
	req.SecurityAssessmentProfile = "tenant-http"
	manifest, err := runWorkflowOpts(t, fake, req, workflowRunOptions{mutateConfig: configureSecurityFixture(t)})
	if err != nil || manifest.FactoryResult != StateVerificationFailed || manifest.VerificationResult != OutcomeFailed {
		t.Fatalf("mandatory failure lost: result=%s verification=%s err=%v", manifest.FactoryResult, manifest.VerificationResult, err)
	}
	if manifest.SecurityAssessment == nil || manifest.SecurityAssessment.Status != "incomplete" || manifest.SecurityAssessment.ErrorCode != "functional_verification_failed" {
		t.Fatalf("failed gates became complete security advice: %+v", manifest.SecurityAssessment)
	}
}

func TestSecurityWorkflowAdviceCannotApproveHumanReview(t *testing.T) {
	for _, decision := range []string{HumanResultRejected, HumanResultTimeout} {
		t.Run(decision, func(t *testing.T) {
			fake := sandbox.NewFake()
			fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
			req := baseRequest(t)
			req.SecurityAssessmentProfile = "tenant-http"
			configure := configureSecurityFixture(t)
			manifest, err := runWorkflowOpts(t, fake, req, workflowRunOptions{mutateConfig: func(cfg *Config) {
				configure(cfg)
				cfg.Review = reviewConfig(time.Minute)
			}}, func(env *testsuite.TestWorkflowEnvironment) {
				// Complete passing advice is deliberately stronger than the real nil-executor path.
				env.OnActivity("AssessSecurity", mock.Anything, mock.Anything).Return(SecurityAssessmentOutput{Summary: SecurityAssessmentSummary{Profile: "tenant-http", Status: "complete", AssertionOutcome: "passed", CleanupStatus: "complete"}}, nil).Once()
				if decision == HumanResultRejected {
					env.RegisterDelayedCallback(func() {
						env.SignalWorkflow(ReviewSignalName, ReviewDecision{Decision: decision, Actor: "independent-reviewer"})
					}, time.Second)
				}
			})
			if err != nil || manifest.HumanResult != decision || manifest.ReviewOutcome != decision || manifest.SecurityAssessment == nil || manifest.SecurityAssessment.Status != "complete" {
				t.Fatalf("advice changed human outcome: %+v err=%v", manifest, err)
			}
			if decision == HumanResultRejected && manifest.FactoryResult != StateRejected {
				t.Fatalf("advice overrode rejection: %s", manifest.FactoryResult)
			}
		})
	}
}

// The exported history stays outside the repository because activity payloads
// can contain private repository metadata. CI/Cube supplies it explicitly.
func TestSecurityReplayPreChangeHistory(t *testing.T) {
	path := os.Getenv("SECURITY_REPLAY_HISTORY")
	if path == "" {
		t.Skip("set SECURITY_REPLAY_HISTORY to a retained pre-change Temporal JSON history; version mocks are not replay evidence")
	}
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(SoftwareChangeWorkflow)
	if err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, path); err != nil {
		t.Fatalf("pre-change history is incompatible: %v", err)
	}
}
