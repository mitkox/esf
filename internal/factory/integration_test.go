//go:build integration

package factory

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/proto"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/verification"
)

// ── A trivial workflow, to prove Temporal works at all ──────────────────────

// HelloWorkflow is the smallest possible durable workflow. It exists to prove
// that the gRPC endpoint is reachable, the namespace exists, and a workflow can
// actually execute — not merely that a container is running.
func HelloWorkflow(ctx workflow.Context, name string) (string, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         nil,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	var greeting string
	if err := workflow.ExecuteActivity(ctx, HelloActivity, name).Get(ctx, &greeting); err != nil {
		return "", err
	}
	return greeting, nil
}

// HelloActivity is the side effect of the trivial workflow.
func HelloActivity(_ context.Context, name string) (string, error) {
	return "hello " + name, nil
}

func temporalClientForTest(t *testing.T) client.Client {
	t.Helper()

	hostPort := envOr("TEMPORAL_HOST_PORT", "127.0.0.1:7233")
	namespace := envOr("TEMPORAL_NAMESPACE", "default")

	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), testCodec(t, "new"))
	c, err := client.Dial(client.Options{
		DataConverter:    dc,
		FailureConverter: temporal.NewDefaultFailureConverter(temporal.DefaultFailureConverterOptions{DataConverter: dc, EncodeCommonAttributes: true}),
		HostPort:         hostPort,
		Namespace:        namespace,
		Logger:           newTemporalLogger(slog.New(slog.DiscardHandler)),
	})
	if err != nil {
		t.Fatalf("dial Temporal at %s: %v (is `make temporal-up` running?)", hostPort, err)
	}
	t.Cleanup(c.Close)
	return c
}

// TestTemporalHelloWorkflow proves a trivial workflow executes end to end.
func TestTemporalHelloWorkflow(t *testing.T) {
	c := temporalClientForTest(t)

	taskQueue := fmt.Sprintf("factory-hello-test-%d", time.Now().UnixNano())
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(HelloWorkflow)
	w.RegisterActivity(HelloActivity)
	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(w.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        fmt.Sprintf("hello-test-%d", time.Now().UnixNano()),
		TaskQueue: taskQueue,
	}, HelloWorkflow, "factory")
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}

	var result string
	if err := run.Get(ctx, &result); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if result != "hello factory" {
		t.Fatalf("result = %q, want %q", result, "hello factory")
	}
}

// ── Full workflow against real Temporal ─────────────────────────────────────

// blockingProvider wraps the fake provider so the agent execution can be held
// open, which is what makes cancellation and timeout observable.
type blockingProvider struct {
	*sandbox.Fake
	blockAgent chan struct{}
	once       sync.Once
	destroyed  chan string
}

func newBlockingProvider() *blockingProvider {
	fake := sandbox.NewFake()
	bp := &blockingProvider{
		Fake:       fake,
		blockAgent: make(chan struct{}),
		destroyed:  make(chan string, 8),
	}
	fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		line := strings.Join(cmd.Argv, " ")
		if strings.Contains(line, "agent run") {
			// Hold the agent open until the test releases it or cancellation
			// tears the workflow down.
			<-bp.blockAgent
		}
		switch {
		case strings.Contains(line, "rev-parse --abbrev-ref"):
			return sandbox.Execution{ExitCode: 0, Stdout: "main\n"}, nil
		case strings.Contains(line, "rev-parse"):
			return sandbox.Execution{ExitCode: 0, Stdout: "2222222222222222222222222222222222222222\n"}, nil
		case strings.Contains(line, "diff --cached"):
			return sandbox.Execution{ExitCode: 0, Stdout: "diff --git a/f b/f"}, nil
		default:
			return sandbox.Execution{ExitCode: 0}, nil
		}
	}
	return bp
}

func (b *blockingProvider) Destroy(_ context.Context, id string) error {
	select {
	case b.destroyed <- id:
	default:
	}
	return b.Fake.Destroy(context.Background(), id)
}

// release lets the agent finish.
func (b *blockingProvider) release() {
	b.once.Do(func() { close(b.blockAgent) })
}

func newIntegrationFixture(t *testing.T, provider sandbox.Provider) (client.Client, string, *artifacts.Local) {
	t.Helper()

	base := Default()
	base.Cube.TemplateID = "tpl-integration"
	base.Sandbox.BasePackages = []string{"git"}

	harness, err := agentharness.NewGeneric(agentharness.Spec{
		Name:       "integration-agent",
		Executable: "agent",
		Args:       []string{"run"},
		Timeout:    5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	registry := agentharness.NewRegistry()
	if err := registry.Register(harness); err != nil {
		t.Fatalf("Register: %v", err)
	}

	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	telemetry, err := NewTelemetry(context.Background(), ObservabilityConfig{})
	if err != nil {
		t.Fatalf("NewTelemetry: %v", err)
	}

	mandatory := true
	base.Verification = map[string]verification.Profile{
		"default": {
			Name:  "default",
			Steps: []verification.Step{{ID: "build", Argv: []string{"./build.sh"}, Mandatory: &mandatory}},
		},
	}

	acts, err := NewActivities(ActivitiesOptions{
		Provider:        provider,
		Repositories:    repository.New(nil),
		Harnesses:       registry,
		Profiles:        base.VerificationProfiles(),
		ArtifactFactory: store,
		Redactor:        NewRedactor(),
		Telemetry:       telemetry,
		Config:          base,
		Logger:          slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewActivities: %v", err)
	}

	c := temporalClientForTest(t)
	taskQueue := fmt.Sprintf("factory-integration-%d", time.Now().UnixNano())

	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(SoftwareChangeWorkflow, workflowRegistrationOptions())
	w.RegisterActivity(acts)
	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(w.Stop)

	return c, taskQueue, store
}

func integrationFixtureRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := execGit(t, dir, args...)
		if cmd != "" {
			t.Log(cmd)
		}
	}
	run("init", "-q")
	run("config", "user.email", "factory@test")
	run("config", "user.name", "Factory")
	if err := os.WriteFile(dir+"/greeting.py", []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	run("add", "-A")
	run("commit", "-qm", "baseline")
	return dir, revParse(t, dir)
}

// TestWorkflowWithRealTemporal proves the full workflow dispatches and completes
// against a real Temporal server, and that the sandbox is destroyed.
func TestWorkflowWithRealTemporal(t *testing.T) {
	bp := newBlockingProvider()
	c, taskQueue, store := newIntegrationFixture(t, bp)

	repoDir, sha := integrationFixtureRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req := RunRequest{
		RunID:               fmt.Sprintf("run-int-%d", time.Now().UnixNano()),
		LocalPath:           repoDir,
		RepositoryKind:      "local",
		Revision:            sha,
		Task:                "change the greeting",
		AgentHarness:        "integration-agent",
		VerificationProfile: "default",
		AgentTimeout:        2 * time.Minute,
	}

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        WorkflowIDForRun(req.RunID),
		TaskQueue: taskQueue,
	}, SoftwareChangeWorkflow, req)
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}

	// Let the agent finish once the sandbox is created and the agent is running.
	go func() {
		time.Sleep(2 * time.Second)
		bp.release()
	}()

	var manifest RunManifest
	if err := run.Get(ctx, &manifest); err != nil {
		t.Fatalf("workflow failed: %v", err)
	}
	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s (error: %s)", manifest.FactoryResult, manifest.Error)
	}
	if manifest.CleanupResult.Outcome != OutcomeSuccess {
		t.Fatalf("cleanup_result = %+v", manifest.CleanupResult)
	}
	if len(bp.Live()) != 0 {
		t.Fatalf("sandbox leaked: %v", bp.Live())
	}

	// Inspect server history directly: prompts must not be stored in plaintext.
	history := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for history.HasNext() {
		event, err := history.Next()
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(req.Task)) {
			t.Fatal("task leaked into Temporal history")
		}
	}

	// The manifest must have been persisted to durable storage.
	if _, err := ReadManifest(store, req.RunID); err != nil {
		t.Fatalf("manifest was not persisted: %v", err)
	}
}

// TestWorkflowCancellationDestroysSandbox covers acceptance CASE D: a cancelled
// workflow must still destroy its sandbox.
func TestWorkflowCancellationDestroysSandbox(t *testing.T) {
	bp := newBlockingProvider()
	c, taskQueue, _ := newIntegrationFixture(t, bp)

	repoDir, sha := integrationFixtureRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req := RunRequest{
		RunID:               fmt.Sprintf("run-cancel-%d", time.Now().UnixNano()),
		LocalPath:           repoDir,
		RepositoryKind:      "local",
		Revision:            sha,
		Task:                "change the greeting",
		AgentHarness:        "integration-agent",
		VerificationProfile: "default",
		AgentTimeout:        5 * time.Minute,
	}

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        WorkflowIDForRun(req.RunID),
		TaskQueue: taskQueue,
	}, SoftwareChangeWorkflow, req)
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}

	// Cancel while the agent is holding the sandbox open.
	time.Sleep(2 * time.Second)
	if err := c.CancelWorkflow(ctx, WorkflowIDForRun(req.RunID), run.GetRunID()); err != nil {
		t.Fatalf("CancelWorkflow: %v", err)
	}

	// The workflow should finish (cancelled) rather than hang.
	_ = run.Get(ctx, &RunManifest{})

	// Cleanup must have run even though the workflow was cancelled.
	deadline := time.After(30 * time.Second)
	for {
		if len(bp.Live()) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("sandbox leaked after cancellation: %v", bp.Live())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// TestWorkflowTimeoutCleansUp covers acceptance CASE C: when the agent exceeds
// its timeout the sandbox is still destroyed.
func TestWorkflowTimeoutCleansUp(t *testing.T) {
	bp := newBlockingProvider()
	// Make the agent exceed a very short timeout.
	bp.Fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		line := strings.Join(cmd.Argv, " ")
		if strings.Contains(line, "agent run") {
			return sandbox.Execution{
				ExitCode:    -1,
				TimedOut:    true,
				StartedAt:   time.Now().UTC(),
				CompletedAt: time.Now().UTC(),
				Stderr:      "agent timed out",
			}, nil
		}
		switch {
		case strings.Contains(line, "rev-parse --abbrev-ref"):
			return sandbox.Execution{ExitCode: 0, Stdout: "main\n"}, nil
		case strings.Contains(line, "rev-parse"):
			return sandbox.Execution{ExitCode: 0, Stdout: "3333333333333333333333333333333333333333\n"}, nil
		default:
			return sandbox.Execution{ExitCode: 0}, nil
		}
	}

	c, taskQueue, _ := newIntegrationFixture(t, bp)
	repoDir, sha := integrationFixtureRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req := RunRequest{
		RunID:               fmt.Sprintf("run-timeout-%d", time.Now().UnixNano()),
		LocalPath:           repoDir,
		RepositoryKind:      "local",
		Revision:            sha,
		Task:                "change the greeting",
		AgentHarness:        "integration-agent",
		VerificationProfile: "default",
		AgentTimeout:        time.Second,
	}

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        WorkflowIDForRun(req.RunID),
		TaskQueue: taskQueue,
	}, SoftwareChangeWorkflow, req)
	if err != nil {
		t.Fatalf("ExecuteWorkflow: %v", err)
	}

	var manifest RunManifest
	if err := run.Get(ctx, &manifest); err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if manifest.FactoryResult != StateAgentFailed {
		t.Fatalf("factory_result = %s, want AGENT_FAILED", manifest.FactoryResult)
	}
	if manifest.AgentResult != OutcomeTimedOut {
		t.Fatalf("agent_result = %s, want TIMED_OUT", manifest.AgentResult)
	}
	if manifest.CleanupResult.Outcome != OutcomeSuccess {
		t.Fatalf("cleanup_result = %+v, want SUCCESS", manifest.CleanupResult)
	}
	if len(bp.Live()) != 0 {
		t.Fatalf("sandbox leaked after timeout: %v", bp.Live())
	}
}

// execGit runs a git command in dir and returns its combined output.
func execGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// revParse returns the HEAD commit of dir.
func revParse(t *testing.T, dir string) string {
	t.Helper()
	return execGit(t, dir, "rev-parse", "HEAD")
}

// This opt-in test crosses the production three-minute heartbeat deadline.
func TestLongAgentHeartbeat(t *testing.T) {
	if os.Getenv("FACTORY_LONG_TESTS") != "1" {
		t.Skip("set FACTORY_LONG_TESTS=1 for the 190-second heartbeat check")
	}
	bp := newBlockingProvider()
	defer bp.release()
	c, queue, _ := newIntegrationFixture(t, bp)
	dir, sha := integrationFixtureRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req := RunRequest{RunID: fmt.Sprintf("run-heartbeat-%d", time.Now().UnixNano()), LocalPath: dir, RepositoryKind: "local", Revision: sha, Task: "heartbeat check", AgentHarness: "integration-agent", VerificationProfile: "default", AgentTimeout: 4 * time.Minute}
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: WorkflowIDForRun(req.RunID), TaskQueue: queue}, SoftwareChangeWorkflow, req)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for the activity to start before measuring the heartbeat interval.
	for {
		status, err := QueryRunStatus(ctx, c, run.GetID(), run.GetRunID())
		if err == nil && status.CurrentStep == "agent.run" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	timer := time.AfterFunc(190*time.Second, bp.release)
	defer timer.Stop()
	var manifest RunManifest
	if err := run.Get(ctx, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FactoryResult != StateSucceeded || !manifest.CleanupResult.Verified {
		t.Fatalf("long agent failed: %+v", manifest)
	}
	if len(bp.Live()) != 0 {
		t.Fatal("sandbox leaked")
	}
}
