//go:build integration

package factory

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/securityassessment"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// The SDK's in-memory RequestCancelActivity resolves its callback immediately,
// regardless of activity WaitForCancellation. This test requires real Temporal
// so that cancellation delivery and teardown ordering are actually observed.
// Execute inside a trusted validator Cube with TEMPORAL_HOST_PORT and
// TEMPORAL_NAMESPACE set for its explicitly permitted test server.
func securityCancellationWorkflow(ctx workflow.Context, in SecurityAssessmentInput) error {
	return executeActivity(ctx, "AssessSecurity", securityAssessmentOptions(), in).Get(ctx, nil)
}

func securityCaptureCancellationWorkflow(ctx workflow.Context, in SecurityAssessmentInput) error {
	err := executeActivity(ctx, "CaptureSecurityCandidate", securityCandidateCaptureOptions(), CaptureSecurityCandidateInput{RunID: in.RunID, SandboxID: in.SandboxID, RepositoryDir: in.RepositoryDir, ProfileConfig: in.ProfileConfig}).Get(ctx, nil)
	disconnected, _ := workflow.NewDisconnectedContext(ctx)
	if cleanupErr := executeActivity(disconnected, "RecordSecurityAssessment", noRetry(), RecordSecurityAssessmentInput{RunID: in.RunID, Summary: SecurityAssessmentSummary{Profile: in.ProfileName, Status: "incomplete", AssertionOutcome: "unobserved"}}).Get(disconnected, nil); cleanupErr != nil {
		return cleanupErr
	}
	return err
}

type cancellationSecurityExecutor struct {
	started, canceled, cleanupAllowed, cleanupAcknowledged chan struct{}
}

func (e *cancellationSecurityExecutor) Assess(ctx context.Context, _ SecurityAssessmentExecution) (SecurityAssessmentExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	close(e.canceled)
	// Model the executor's detached, bounded Cube/broker/guard teardown. The
	// cancellation context cannot bypass this acknowledgment checkpoint.
	<-e.cleanupAllowed
	close(e.cleanupAcknowledged)
	return SecurityAssessmentExecutionResult{}, ctx.Err()
}

func cancellationSecurityFixture(t *testing.T, executor SecurityAssessmentExecutor) (*Activities, SecurityAssessmentInput) {
	t.Helper()
	var archive bytes.Buffer
	if err := tar.NewWriter(&archive).Close(); err != nil {
		t.Fatal(err)
	}
	manifest := securityassessment.CandidateManifest{Schema: securityassessment.CandidateSchema, ArchiveSHA256: securityassessment.SHA256(archive.Bytes())}
	digestInput, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = securityassessment.SHA256(digestInput)
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	provider := sandbox.NewFake()
	sb, err := provider.Create(context.Background(), sandbox.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	provider.ExecuteFunc = func(command sandbox.Command) (sandbox.Execution, error) {
		if command.Description == "freeze exact candidate bytes" {
			if err := sb.WriteFile(context.Background(), command.Argv[5], archive.Bytes()); err != nil {
				return sandbox.Execution{}, err
			}
			inventory, err := json.Marshal(struct {
				Manifest      securityassessment.CandidateManifest `json:"manifest"`
				ArchiveSHA256 string                               `json:"archive_sha256"`
			}{manifest, manifest.ArchiveSHA256})
			if err != nil {
				return sandbox.Execution{}, err
			}
			return sandbox.Execution{}, sb.WriteFile(context.Background(), command.Argv[6], inventory)
		}
		return sandbox.Execution{}, nil
	}
	dir := t.TempDir()
	factory, err := artifacts.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	acts := &Activities{provider: provider, artifactFactory: factory, securityAssessor: executor, redactor: NewRedactor(), cfg: Config{Storage: StorageConfig{DataDir: dir}}}
	input := SecurityAssessmentInput{RunID: "cancellation-run", SandboxID: sb.ID(), RepositoryDir: "/application", ProfileName: "tenant-http", ProfileConfig: securityProfileFixture(t), FunctionalPassed: true,
		Snapshot: SecurityCandidateReference{Ref: "candidate-v1", CandidateDigest: manifest.Digest, ArchiveSHA256: manifest.ArchiveSHA256, ManifestSHA256: securityassessment.SHA256(manifestBytes)}}
	if err := acts.writeSecurityPrivate(input.RunID, "candidate/manifest.json", manifestBytes); err != nil {
		t.Fatal(err)
	}
	if err := acts.writeSecurityPrivate(input.RunID, "candidate/archive.tar", archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	return acts, input
}

func TestSecurityTemporalCancellationWaitsForExecutorCleanup(t *testing.T) {
	executor := &cancellationSecurityExecutor{started: make(chan struct{}), canceled: make(chan struct{}), cleanupAllowed: make(chan struct{}), cleanupAcknowledged: make(chan struct{})}
	acts, input := cancellationSecurityFixture(t, executor)
	runSecurityCancellationLifecycle(t, securityCancellationWorkflow, acts, input, executor)
}

type cancellationCaptureProvider struct {
	sandbox.Provider
	gate *cancellationSecurityExecutor
}

func (p *cancellationCaptureProvider) Reattach(ctx context.Context, id string) (sandbox.Sandbox, error) {
	sb, err := p.Provider.Reattach(ctx, id)
	if err != nil {
		return nil, err
	}
	return &cancellationCaptureSandbox{Sandbox: sb, gate: p.gate}, nil
}

type cancellationCaptureSandbox struct {
	sandbox.Sandbox
	gate *cancellationSecurityExecutor
}

func (s *cancellationCaptureSandbox) Execute(ctx context.Context, command sandbox.Command) (sandbox.Execution, error) {
	if command.Description == "freeze exact candidate bytes" {
		close(s.gate.started)
		<-ctx.Done()
		close(s.gate.canceled)
		<-s.gate.cleanupAllowed
		// Simulate a remote scan that finishes after receiving cancellation.
		// The real capture activity must not leave its resulting source behind.
		return s.Sandbox.Execute(context.WithoutCancel(ctx), command)
	}
	result, err := s.Sandbox.Execute(ctx, command)
	if command.Description == "remove private candidate export" {
		close(s.gate.cleanupAcknowledged)
	}
	return result, err
}

func (s *cancellationCaptureSandbox) ReadFileBounded(ctx context.Context, name string, max int64) ([]byte, error) {
	return s.Sandbox.(sandbox.BoundedFileReader).ReadFileBounded(context.WithoutCancel(ctx), name, max)
}

func TestSecurityTemporalCancellationWaitsForCaptureCleanup(t *testing.T) {
	gate := &cancellationSecurityExecutor{started: make(chan struct{}), canceled: make(chan struct{}), cleanupAllowed: make(chan struct{}), cleanupAcknowledged: make(chan struct{})}
	acts, input := cancellationSecurityFixture(t, nil)
	acts.provider = &cancellationCaptureProvider{Provider: acts.provider, gate: gate}
	runSecurityCancellationLifecycle(t, securityCaptureCancellationWorkflow, acts, input, gate)
}

func runSecurityCancellationLifecycle(t *testing.T, workflowFn any, acts *Activities, input SecurityAssessmentInput, executor *cancellationSecurityExecutor) {
	t.Helper()
	c := temporalClientForTest(t)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(executor.cleanupAllowed) }) }
	defer release()
	queue := fmt.Sprintf("security-cancellation-%d", time.Now().UnixNano())
	w := worker.New(c, queue, worker.Options{MaxHeartbeatThrottleInterval: 100 * time.Millisecond})
	w.RegisterWorkflow(workflowFn)
	w.RegisterActivity(acts.AssessSecurity)
	w.RegisterActivity(acts.CaptureSecurityCandidate)
	w.RegisterActivity(acts.RecordSecurityAssessment)
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { release(); w.Stop() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: queue, TaskQueue: queue}, workflowFn, input)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = c.CancelWorkflow(cleanupCtx, run.GetID(), run.GetRunID())
	}()
	select {
	case <-executor.started:
	case <-ctx.Done():
		t.Fatal("assessment executor did not start")
	}
	if err := c.CancelWorkflow(ctx, run.GetID(), run.GetRunID()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.canceled:
	case <-ctx.Done():
		t.Fatal("assessment heartbeat did not deliver cancellation")
	}
	completed := make(chan error, 1)
	go func() { completed <- run.Get(ctx, nil) }()
	select {
	case err := <-completed:
		t.Fatalf("workflow completed before executor cleanup acknowledgment: %v", err)
	case <-time.After(time.Second):
	}
	store, err := acts.artifactFactory.ForRun(input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := store.Exists(ArtifactSecurityAssessment); err != nil || exists {
		t.Fatalf("fallback/advisory persistence raced unfinished cleanup: exists=%v, err=%v", exists, err)
	}
	release()
	select {
	case err := <-completed:
		if !temporal.IsCanceledError(err) {
			t.Fatalf("workflow did not preserve cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("workflow did not finish after executor cleanup acknowledgment")
	}
	select {
	case <-executor.cleanupAcknowledged:
	default:
		t.Fatal("workflow advanced before executor acknowledged cleanup")
	}
	if _, err := os.Stat(filepath.Join(acts.cfg.Storage.DataDir, "security-private", input.RunID, "candidate")); !os.IsNotExist(err) {
		t.Fatalf("canceled security activity retained private source: %v", err)
	}
}
