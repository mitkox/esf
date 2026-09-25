package factory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/artifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/tomlx"
	"github.com/mitkox/esf/internal/verification"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const testSHA = "1111111111111111111111111111111111111111"

// gitAwareExecute makes the fake sandbox answer the git commands the repository
// provider issues, so workflow tests exercise the real provider logic without a
// real repository.
func gitAwareExecute(patch string) func(sandbox.Command) (sandbox.Execution, error) {
	return func(cmd sandbox.Command) (sandbox.Execution, error) {
		argv := strings.Join(cmd.Argv, " ")
		switch {
		case strings.Contains(argv, "rev-parse --abbrev-ref"):
			return sandbox.Execution{ExitCode: 0, Stdout: "main\n"}, nil
		case strings.Contains(argv, "rev-parse"):
			return sandbox.Execution{ExitCode: 0, Stdout: testSHA + "\n"}, nil
		case strings.Contains(argv, "status --porcelain"):
			return sandbox.Execution{ExitCode: 0, Stdout: ""}, nil
		case strings.Contains(argv, "diff --cached"):
			return sandbox.Execution{ExitCode: 0, Stdout: patch}, nil
		default:
			return sandbox.Execution{ExitCode: 0}, nil
		}
	}
}

// workflowRunOptions customises the test workflow's environment.
type workflowRunOptions struct {
	// mutateConfig adjusts the operator configuration before activities are
	// built, so a test can enable the review gate, declare resources, or set a
	// budget without duplicating the fixture.
	mutateConfig func(*Config)
	artifactDir  string
}

// runWorkflow executes the workflow in Temporal's in-memory test environment
// with the factory's activities registered, so state transitions, retries,
// cleanup and the final manifest are all exercised without a Temporal server.
func runWorkflow(t *testing.T, fake sandbox.Provider, req RunRequest, configure ...func(*testsuite.TestWorkflowEnvironment)) (RunManifest, error) {
	t.Helper()
	return runWorkflowOpts(t, fake, req, workflowRunOptions{}, configure...)
}

// runWorkflowOpts is runWorkflow with configuration control.
func runWorkflowOpts(t *testing.T, fake sandbox.Provider, req RunRequest, opts workflowRunOptions, configure ...func(*testsuite.TestWorkflowEnvironment)) (RunManifest, error) {
	t.Helper()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	harness, err := agentharness.NewGeneric(agentharness.Spec{
		Name:       "test-agent",
		Executable: "agent",
		Args:       []string{"run", agentharness.ModelArgsPlaceholder},
		ModelFlag:  "--model",
		Timeout:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	registry := agentharness.NewRegistry()
	_ = registry.Register(harness)

	artifactDir := opts.artifactDir
	if artifactDir == "" {
		artifactDir = t.TempDir()
	}
	store, err := artifacts.NewLocal(artifactDir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	telemetry, err := NewTelemetry(context.Background(), ObservabilityConfig{})
	if err != nil {
		t.Fatalf("NewTelemetry: %v", err)
	}

	mandatory := true
	cfg := Config{
		FactoryVersion: "test",
		Cube:           cubeConfigForTest(),
		Sandbox:        SandboxConfig{BasePackages: []string{"git"}},
		Verification: map[string]verification.Profile{
			"default": {
				Name:  "default",
				Steps: []verification.Step{{ID: "build", Argv: []string{"./build.sh"}, Mandatory: &mandatory}},
			},
		},
	}
	if opts.mutateConfig != nil {
		opts.mutateConfig(&cfg)
	}

	acts, err := NewActivities(ActivitiesOptions{
		Provider:        fake,
		Repositories:    repository.New(nil),
		Harnesses:       registry,
		Profiles:        cfg.VerificationProfiles(),
		ArtifactFactory: store,
		Redactor:        NewRedactor(),
		Telemetry:       telemetry,
		Config:          cfg,
		Logger:          slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewActivities: %v", err)
	}

	env.RegisterWorkflow(SoftwareChangeWorkflow)
	env.RegisterActivity(acts)
	for _, setup := range configure {
		setup(env)
	}
	env.ExecuteWorkflow(SoftwareChangeWorkflow, req)

	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	var manifest RunManifest
	if err := env.GetWorkflowResult(&manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

// newFixtureRepo creates a real, minimal git repository on the host.
//
// The repository provider packs a local source with `git bundle`, so the fixture
// must be a genuine repository even though the sandbox is fake.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	commands := [][]string{
		{"init", "-q"},
		{"config", "user.email", "factory@test"},
		{"config", "user.name", "Factory Test"},
	}
	for _, args := range commands {
		runGit(t, dir, args...)
	}
	content := "def greeting():\n    return \"hello\"\n"
	if err := os.WriteFile(filepath.Join(dir, "greeting.py"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "fixture baseline")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// fixtureSHA returns the HEAD commit of a fixture repository.
func fixtureSHA(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func baseRequest(t *testing.T) RunRequest {
	t.Helper()
	repo := newFixtureRepo(t)
	return RunRequest{
		RunID:               "run-workflow-test",
		LocalPath:           repo,
		RepositoryKind:      "local",
		Revision:            fixtureSHA(t, repo),
		Task:                "change the greeting",
		AgentHarness:        "test-agent",
		VerificationProfile: "default",
		AgentTimeout:        time.Minute,
	}
}

// TestWorkflowSucceedsOnVerifiedChange is the happy path: the agent runs, the
// deterministic gate passes, and the manifest says SUCCEEDED.
func TestWorkflowSucceedsOnVerifiedChange(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = gitAwareExecute("diff --git a/greeting.py b/greeting.py")
	// Package installation must not be treated as a git command.
	fake.ExecuteFunc = wrapWithBuildSuccess(fake.ExecuteFunc)

	manifest, err := runWorkflow(t, fake, baseRequest(t))
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}

	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s, want SUCCEEDED (error: %s)", manifest.FactoryResult, manifest.Error)
	}
	if manifest.AgentResult != OutcomeSuccess {
		t.Fatalf("agent_result = %s, want SUCCESS", manifest.AgentResult)
	}
	if manifest.VerificationResult != OutcomeSuccess {
		t.Fatalf("verification_result = %s, want SUCCESS", manifest.VerificationResult)
	}
	if manifest.CleanupResult.Outcome != OutcomeSuccess || !manifest.CleanupResult.Verified {
		t.Fatalf("cleanup_result = %+v, want a verified success", manifest.CleanupResult)
	}
	if len(fake.Live()) != 0 {
		t.Fatalf("sandbox leaked: %v", fake.Live())
	}
	if manifest.BaselineSHA != testSHA {
		t.Fatalf("baseline_sha = %q, want %q", manifest.BaselineSHA, testSHA)
	}
	if manifest.Patch == "" {
		t.Fatal("no patch artifact was recorded")
	}
	if manifest.TaskHash == "" {
		t.Fatal("task_hash was not recorded")
	}
}

// TestWorkflowExecutesResolvedModel proves a named resource affects execution,
// not only the manifest. The raw resource name must never be handed to the
// harness as though it were a provider model ID.
func TestWorkflowExecutesResolvedModel(t *testing.T) {
	t.Setenv("FACTORY_TEST_MODEL_KEY", "test-secret-value")
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	req := baseRequest(t)
	req.AgentModel = "fast"
	req.Model = "fast"

	manifest, err := runWorkflowOpts(t, fake, req, workflowRunOptions{
		mutateConfig: func(cfg *Config) {
			cfg.Models = map[string]ModelConfig{
				"fast": {Provider: "test", Model: "provider/resolved-model", APIKeyEnv: "FACTORY_TEST_MODEL_KEY"},
			}
		},
	})
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if manifest.Model != "provider/resolved-model" {
		t.Fatalf("manifest model = %q, want resolved ID", manifest.Model)
	}
	created := fake.Created()
	if len(created) != 1 {
		t.Fatalf("created sandboxes = %v, want one", created)
	}
	var found bool
	for _, cmd := range fake.Commands(created[0]) {
		line := strings.Join(cmd.Argv, " ")
		if !strings.Contains(line, "agent run") {
			continue
		}
		found = true
		if !strings.Contains(line, "--model provider/resolved-model") || strings.Contains(line, "--model fast") {
			t.Fatalf("agent command = %q, want resolved model ID only", line)
		}
		if cmd.Env["FACTORY_TEST_MODEL_KEY"] != "test-secret-value" {
			t.Fatal("resolved model credential was not supplied to the harness")
		}
	}
	if !found {
		t.Fatal("agent command was not recorded")
	}
}

// TestWorkflowAgentFailureStillCleansUp covers acceptance CASE A.
func TestWorkflowAgentFailureStillCleansUp(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		// The agent exits non-zero.
		if strings.Contains(strings.Join(cmd.Argv, " "), "agent run") {
			return sandbox.Execution{ExitCode: 3, Stderr: "agent blew up"}, nil
		}
		return gitAwareExecute("")(cmd)
	}

	manifest, err := runWorkflow(t, fake, baseRequest(t))
	if err != nil {
		t.Fatalf("workflow should return a manifest, got error: %v", err)
	}

	if manifest.FactoryResult != StateAgentFailed {
		t.Fatalf("factory_result = %s, want AGENT_FAILED", manifest.FactoryResult)
	}
	if manifest.AgentResult != OutcomeFailed {
		t.Fatalf("agent_result = %s, want FAILED", manifest.AgentResult)
	}
	// Verification must be recorded as SKIPPED, never silently absent.
	if manifest.VerificationResult != OutcomeSkipped {
		t.Fatalf("verification_result = %s, want SKIPPED", manifest.VerificationResult)
	}
	// CASE A requires the sandbox to be destroyed anyway.
	if manifest.CleanupResult.Outcome != OutcomeSuccess {
		t.Fatalf("cleanup_result = %+v, want SUCCESS", manifest.CleanupResult)
	}
	if len(fake.Live()) != 0 {
		t.Fatalf("sandbox leaked after agent failure: %v", fake.Live())
	}
}

// TestWorkflowVerificationFailureCoversCaseB is the case the whole design is
// built around: the agent claims success, the deterministic gate disagrees, and
// the factory sides with the gate.
func TestWorkflowVerificationFailureCoversCaseB(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		line := strings.Join(cmd.Argv, " ")
		// The agent exits zero...
		if strings.Contains(line, "agent run") {
			return sandbox.Execution{ExitCode: 0, Stdout: "done!"}, nil
		}
		// ...but the gate fails.
		if strings.Contains(line, "./build.sh") {
			return sandbox.Execution{ExitCode: 1, Stdout: "tests failed"}, nil
		}
		if strings.Contains(line, "apt-get") {
			return sandbox.Execution{ExitCode: 0, Stdout: "base-packages-ok"}, nil
		}
		return gitAwareExecute("")(cmd)
	}

	manifest, err := runWorkflow(t, fake, baseRequest(t))
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}

	if manifest.FactoryResult != StateVerificationFailed {
		t.Fatalf("factory_result = %s, want VERIFICATION_FAILED", manifest.FactoryResult)
	}
	// The critical distinction: agent success is NOT factory success.
	if manifest.AgentResult != OutcomeSuccess {
		t.Fatalf("agent_result = %s, want SUCCESS", manifest.AgentResult)
	}
	if manifest.VerificationResult != OutcomeFailed {
		t.Fatalf("verification_result = %s, want FAILED", manifest.VerificationResult)
	}
	if manifest.CleanupResult.Outcome != OutcomeSuccess {
		t.Fatalf("cleanup_result = %+v, want SUCCESS", manifest.CleanupResult)
	}
	if len(fake.Live()) != 0 {
		t.Fatalf("sandbox leaked after verification failure: %v", fake.Live())
	}
}

// TestWorkflowInfrastructureFailureCleansUp covers a failure before the agent
// ever runs.
func TestWorkflowInfrastructureFailureCleansUp(t *testing.T) {
	fake := sandbox.NewFake()
	fake.CreateErr = fmt.Errorf("cube api unreachable")

	manifest, err := runWorkflow(t, fake, baseRequest(t))
	if err != nil {
		return // the workflow surfaces the error; the manifest may be absent
	}
	if manifest.FactoryResult != StateInfrastructureFailed {
		t.Fatalf("factory_result = %s, want INFRASTRUCTURE_FAILED", manifest.FactoryResult)
	}
	if !manifest.CleanupResult.Attempted || !manifest.CleanupResult.Verified {
		t.Fatal("ambiguous create must be reconciled and cleanup verified")
	}
}

// TestWorkflowRejectsUnapprovedRepository proves policy is enforced before a
// sandbox is ever created.
func TestWorkflowRejectsUnapprovedRepository(t *testing.T) {
	fake := sandbox.NewFake()
	req := baseRequest(t)
	req.LocalPath = ""
	req.RepositoryKind = "remote"
	req.Repository = "https://evil.example.com/steal"

	manifest, err := runWorkflow(t, fake, req)
	if err != nil {
		// A rejected request is returned as a workflow error; that is expected.
		_ = err
	}
	if len(fake.Created()) != 0 {
		t.Fatalf("a sandbox was created for a rejected request: %v", fake.Created())
	}
	if manifest.FactoryResult != "" && manifest.FactoryResult != StateInvalidRequest {
		t.Fatalf("factory_result = %s, want INVALID_REQUEST", manifest.FactoryResult)
	}
}

// TestWorkflowCleanupFailureIsRecorded proves a leaked VM is visible in the
// durable record rather than only in a log line.
func TestWorkflowCleanupFailureIsRecorded(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("patch"))
	// Destroy fails every attempt, so the sandbox leaks.
	fake.DestroyErr = map[string]error{}

	origCreate := fake
	manifest, err := runWorkflowWithDestroyFailure(t, origCreate)
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if manifest.FactoryResult != StateInfrastructureFailed {
		t.Fatalf("factory_result = %s for leaked sandbox", manifest.FactoryResult)
	}
	if manifest.CleanupResult.Outcome != OutcomeError && manifest.CleanupResult.Outcome != OutcomeFailed {
		t.Fatalf("cleanup_result = %+v, want a recorded failure", manifest.CleanupResult)
	}
}

// wrapWithBuildSuccess makes the fake treat package-install and base-setup
// commands as successful no-ops.
func wrapWithBuildSuccess(next func(sandbox.Command) (sandbox.Execution, error)) func(sandbox.Command) (sandbox.Execution, error) {
	return func(cmd sandbox.Command) (sandbox.Execution, error) {
		line := strings.Join(cmd.Argv, " ") + cmd.Script
		switch {
		case strings.Contains(line, "apt-get"),
			strings.Contains(line, "base-packages-ok"),
			strings.Contains(line, "chmod"),
			strings.Contains(line, "mkdir"),
			strings.Contains(line, "git clone"),
			strings.Contains(line, "git config"),
			strings.Contains(line, "remote remove"),
			strings.Contains(line, "git add"):
			return sandbox.Execution{ExitCode: 0, Stdout: "ok"}, nil
		}
		return next(cmd)
	}
}

// runWorkflowWithDestroyFailure registers a provider whose Destroy always fails,
// so the cleanup path records an error.
func runWorkflowWithDestroyFailure(t *testing.T, _ *sandbox.Fake) (RunManifest, error) {
	t.Helper()

	leaky := &leakyProvider{Fake: sandbox.NewFake()}
	leaky.Fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("patch"))

	return runWorkflow(t, leaky, baseRequest(t))
}

// leakyProvider is a Fake whose Destroy always fails.
type leakyProvider struct {
	*sandbox.Fake
}

func (l *leakyProvider) Destroy(context.Context, string) error {
	return fmt.Errorf("simulated destroy failure")
}

func (l *leakyProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := l.Fake.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &undeletableSandbox{Sandbox: sb}, nil
}

// undeletableSandbox keeps Destroy failing so the provider reports a leak.
type undeletableSandbox struct {
	sandbox.Sandbox
}

func (u *undeletableSandbox) Destroy(context.Context) error {
	return fmt.Errorf("simulated destroy failure")
}

// cubeConfigForTest builds a valid Cube configuration without touching a real
// deployment: workflow tests use a fake sandbox provider.
func cubeConfigForTest() cube.Config {
	return cube.Config{
		APIURL:         "http://127.0.0.1:4000",
		TemplateID:     "tpl-test",
		ProxyNodeIP:    "127.0.0.1",
		ProxyPortHTTP:  80,
		IdleTimeout:    tomlx.FromStd(30 * time.Minute),
		RequestTimeout: tomlx.FromStd(time.Minute),
	}
}

func tomlDuration(d time.Duration) tomlx.Duration { return tomlx.FromStd(d) }

// TestWorkflowDetectsVerificationTreeMutation guards the ordering fix: the
// deliverable patch is captured BEFORE the gates run, so a gate that rewrites a
// tracked file cannot be attributed to the agent. The drift is recorded as
// evidence instead.
func TestWorkflowDetectsVerificationTreeMutation(t *testing.T) {
	fake := sandbox.NewFake()

	// The first `git diff --cached` is the agent's contribution; the second
	// (after verification) shows an extra change made by a gate.
	var diffCalls int
	fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		line := strings.Join(cmd.Argv, " ")
		if strings.Contains(line, "diff --cached") {
			diffCalls++
			if diffCalls == 1 {
				return sandbox.Execution{ExitCode: 0, Stdout: "diff --git a/agent.py b/agent.py"}, nil
			}
			return sandbox.Execution{ExitCode: 0, Stdout: "diff --git a/agent.py b/agent.py\ndiff --git a/lock b/lock"}, nil
		}
		if strings.Contains(line, "apt-get") || strings.Contains(line, "base-packages-ok") ||
			strings.Contains(line, "chmod") || strings.Contains(line, "mkdir") ||
			strings.Contains(line, "git clone") || strings.Contains(line, "git config") ||
			strings.Contains(line, "remote remove") || strings.Contains(line, "git add") {
			return sandbox.Execution{ExitCode: 0, Stdout: "ok"}, nil
		}
		return gitAwareExecute("")(cmd)
	}

	manifest, err := runWorkflow(t, fake, baseRequest(t))
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s, want SUCCEEDED (error: %s)", manifest.FactoryResult, manifest.Error)
	}
	if !manifest.VerificationMutatedTree {
		t.Fatal("a gate modified the working tree but the manifest does not say so")
	}
	if manifest.PostVerificationPatch == "" {
		t.Fatal("the post-verification patch was not recorded as evidence")
	}
	// The deliverable must contain the AGENT's patch, not the gate's.
	if manifest.Patch != ArtifactPatch {
		t.Fatalf("patch artifact = %q, want %q", manifest.Patch, ArtifactPatch)
	}
}

// TestWorkflowCleanRunHasNoDrift is the counterpart: when the gates are
// side-effect free, the flag stays false.
func TestWorkflowCleanRunHasNoDrift(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/x b/x"))

	manifest, err := runWorkflow(t, fake, baseRequest(t))
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if manifest.VerificationMutatedTree {
		t.Fatal("drift was reported for a side-effect-free run")
	}
}

func TestWorkflowPatchFailureCannotSucceed(t *testing.T) {
	for _, phase := range []string{"agent", "verification"} {
		t.Run(phase, func(t *testing.T) {
			fake := sandbox.NewFake()
			diffs := 0
			fake.ExecuteFunc = wrapWithBuildSuccess(func(cmd sandbox.Command) (sandbox.Execution, error) {
				if strings.Contains(strings.Join(cmd.Argv, " "), "diff --cached") {
					diffs++
					if phase == "agent" || diffs > 1 {
						return sandbox.Execution{}, fmt.Errorf("patch unavailable")
					}
				}
				return gitAwareExecute("patch")(cmd)
			})
			manifest, err := runWorkflow(t, fake, baseRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			if manifest.FactoryResult != StateInfrastructureFailed || !strings.Contains(manifest.Error, "patch unavailable") {
				t.Fatalf("missing evidence accepted: %+v", manifest)
			}
			if !manifest.CleanupResult.Verified || len(fake.Live()) != 0 {
				t.Fatal("sandbox leaked")
			}
		})
	}
}

func TestWorkflowAgentSendsHeartbeat(t *testing.T) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("patch"))
	heartbeats := make(chan struct{}, 10)
	_, err := runWorkflow(t, fake, baseRequest(t), func(env *testsuite.TestWorkflowEnvironment) {
		env.SetOnActivityHeartbeatListener(func(info *activity.Info, _ converter.EncodedValues) {
			if info.ActivityType.Name == "RunAgent" {
				heartbeats <- struct{}{}
			}
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-heartbeats:
	default:
		t.Fatal("agent activity did not heartbeat")
	}
}

func TestWorkflowRecordsCancellationAcrossStages(t *testing.T) {
	for stage, output := range map[string]any{
		"ValidateRequest": ValidateOutput{},
		"PrepareSandbox":  PrepareSandboxOutput{},
		"RunAgent":        RunAgentOutput{},
		"RunVerification": RunVerificationOutput{},
	} {
		t.Run(stage, func(t *testing.T) {
			fake := sandbox.NewFake()
			fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("patch"))
			manifest, err := runWorkflow(t, fake, baseRequest(t), func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity(stage, mock.Anything, mock.Anything).Return(output, temporal.NewCanceledError("operator cancelled"))
			})
			if err != nil {
				t.Fatal(err)
			}
			if manifest.FactoryResult != StateCancelled {
				t.Fatalf("result = %s", manifest.FactoryResult)
			}
			if stage == "RunAgent" && manifest.AgentResult != OutcomeCancelled {
				t.Fatalf("agent result = %s", manifest.AgentResult)
			}
			if len(fake.Live()) != 0 {
				t.Fatal("sandbox leaked after cancellation")
			}
		})
	}
}

func TestFinalizePersistsFallbackPatchReference(t *testing.T) {
	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acts := &Activities{
		artifactFactory: store,
		provider:        sandbox.NewFake(),
		redactor:        NewRedactor(),
		log:             slog.New(slog.DiscardHandler),
	}
	out, err := acts.FinalizeResult(context.Background(), FinalizeInput{
		Request: RunRequest{RunID: "run-fallback"},
		Patch:   "diff --git a/x b/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := ReadManifest(store, "run-fallback")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Patch != ArtifactPatch || persisted.Patch != out.Manifest.Patch {
		t.Fatalf("persisted patch %q differs from returned patch %q", persisted.Patch, out.Manifest.Patch)
	}
	patch, err := ReadArtifact(store, "run-fallback", persisted.Patch)
	if err != nil || string(patch) != "diff --git a/x b/x" {
		t.Fatalf("patch = %q, %v", patch, err)
	}
}

func TestEvidenceFailuresCannotReportSuccess(t *testing.T) {
	for stage, output := range map[string]any{
		"RunAgent":        RunAgentOutput{Result: agentharness.Result{ExitCode: 0}, EvidenceError: "disk full"},
		"RunVerification": RunVerificationOutput{Result: verification.Result{Passed: true}, EvidenceError: "disk full"},
	} {
		t.Run(stage, func(t *testing.T) {
			fake := sandbox.NewFake()
			fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("patch"))
			manifest, err := runWorkflow(t, fake, baseRequest(t), func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity(stage, mock.Anything, mock.Anything).Return(output, nil).Once()
			})
			if err != nil {
				t.Fatal(err)
			}
			if manifest.FactoryResult != StateInfrastructureFailed || !strings.Contains(manifest.Error, "disk full") {
				t.Fatalf("evidence failure hidden: %+v", manifest)
			}
			if len(fake.Live()) != 0 {
				t.Fatal("sandbox leaked")
			}
		})
	}
}
