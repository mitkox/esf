package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/connector"
	"github.com/mitkox/esf/internal/factory"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/forge/azuredevops"
)

type recordingIntakeSink struct {
	mu         sync.Mutex
	proposals  []forge.Proposal
	rejections []forge.Rejection
}

func (s *recordingIntakeSink) Propose(_ context.Context, proposal forge.Proposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proposals = append(s.proposals, proposal)
	return nil
}

func (s *recordingIntakeSink) Reject(_ context.Context, rejection forge.Rejection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejections = append(s.rejections, rejection)
	return nil
}

func (s *recordingIntakeSink) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.proposals), len(s.rejections)
}

func TestRootCommandIncludesConnector(t *testing.T) {
	t.Parallel()

	command, _, err := rootCommand().Find([]string{"connector"})
	if err != nil {
		t.Fatalf("Find(connector): %v", err)
	}
	if command.Name() != "connector" || command.Short == "" {
		t.Fatalf("connector command = %#v", command)
	}
}

func TestConnectorCommandRequiresConfiguredConnector(t *testing.T) {
	t.Setenv("CUBE_API_URL", "http://127.0.0.1:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-test")

	path := filepath.Join(t.TempDir(), "factory.toml")
	if err := os.WriteFile(path, []byte("[hardening]\nacknowledge_open_egress = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newConnectorCommand(&path)
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no connectors are enabled") {
		t.Fatalf("Execute = %v, want a missing-connector error", err)
	}
}

func TestConnectorCommandRejectsInvalidRuntimeConfig(t *testing.T) {
	t.Setenv("CUBE_API_URL", "http://127.0.0.1:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-test")

	path := filepath.Join(t.TempDir(), "factory.toml")
	body := `
[hardening]
acknowledge_open_egress = true

[connectors.azdo]
kind = "azuredevops"
base_url = "https://dev.azure.com/acme"
token_file = "/run/secrets/azdo.token"
webhook_listen = "0.0.0.0:8091"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newConnectorCommand(&path)
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("Execute = %v, want a listener TLS error", err)
	}
}

// TestConnectorWebhookAuthenticatesAndBoundsOverListener exercises the receiver
// the command builds through a real HTTP listener. Authentication is checked
// before parsing, and the body bound is checked before any forge call.
func TestConnectorWebhookAuthenticatesAndBoundsOverListener(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "azdo.token")
	if err := os.WriteFile(tokenFile, []byte("esf:pat"), 0o600); err != nil {
		t.Fatal(err)
	}
	webhookSecretFile := filepath.Join(dir, "azdo-webhook.secret")
	if err := os.WriteFile(webhookSecretFile, []byte("hook-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := factory.Default()
	cfg.Storage.DataDir = dir
	cfg.Connectors = map[string]factory.ConnectorConfig{
		"azdo": {
			Kind:              "azuredevops",
			BaseURL:           "http://127.0.0.1:1",
			TokenFile:         tokenFile,
			WebhookUsername:   "esf-hooks",
			WebhookSecretFile: webhookSecretFile,
			WebhookListen:     "127.0.0.1:8091",
		},
	}
	sink := &recordingIntakeSink{}
	specs, err := buildConnectorRuntime(cfg, sink, nil)
	if err != nil {
		t.Fatalf("buildConnectorRuntime: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	webhook, ok := specs[0].WebhookHandler.(*azuredevops.Webhook)
	if !ok {
		t.Fatalf("handler = %T, want *azuredevops.Webhook", specs[0].WebhookHandler)
	}
	server := httptest.NewServer(webhook)
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/hooks/azuredevops", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("esf", "wrong")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("unauthenticated POST: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", response.StatusCode)
	}

	request, err = http.NewRequest(http.MethodPost, server.URL+"/hooks/azuredevops", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("esf", "pat")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("outbound-credential POST: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("outbound-credential status = %d, want 401", response.StatusCode)
	}

	oversized := bytes.Repeat([]byte("x"), 1<<20+1)
	request, err = http.NewRequest(http.MethodPost, server.URL+"/hooks/azuredevops", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("esf-hooks", "hook-secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("oversized POST: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", response.StatusCode)
	}
	if stats := webhook.Stats(); stats.Unauthorized != 2 {
		t.Fatalf("stats = %#v, want two authentication rejections", stats)
	}
	if proposals, rejections := sink.counts(); proposals != 0 || rejections != 0 {
		t.Fatalf("proposals = %d, rejections = %d, want none", proposals, rejections)
	}
}

type connectorSweeperFunc func(context.Context) (forge.SweepResult, error)

func (f connectorSweeperFunc) Sweep(ctx context.Context) (forge.SweepResult, error) { return f(ctx) }

func (connectorSweeperFunc) CommitSweep(context.Context, string) error { return nil }

// TestConnectorHealthObserverWritesSweepSnapshot runs the real connector
// runtime and the production health adapter, then reads the durable snapshot
// the console and metrics consume.
func TestConnectorHealthObserverWritesSweepSnapshot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	artifactFactory, err := artifacts.NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	telemetry, err := factory.NewTelemetry(context.Background(), factory.ObservabilityConfig{})
	if err != nil {
		t.Fatalf("NewTelemetry: %v", err)
	}
	defer telemetry.Shutdown(context.Background())
	reporter, err := factory.NewConnectorHealthReporter(factory.Default(), artifactFactory, telemetry, nil)
	if err != nil {
		t.Fatalf("NewConnectorHealthReporter: %v", err)
	}
	observe := connectorHealthObserver(reporter)
	observed := make(chan struct{}, 1)
	runtime, err := connector.New([]connector.Spec{{
		Name:          "azdo",
		SweepInterval: time.Hour,
		Sweeper: connectorSweeperFunc(func(context.Context) (forge.SweepResult, error) {
			return forge.SweepResult{}, nil
		}),
		Deliver:       func(context.Context, forge.Candidate) error { return nil },
		WebhookListen: "127.0.0.1:0",
		WebhookHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		}),
		Subscription: func(context.Context) (forge.SubscriptionState, string) {
			return forge.SubscriptionActive, "1 subscription enabled"
		},
		Observe: func(ctx context.Context, observation connector.HealthObservation) error {
			err := observe(ctx, observation)
			if observation.Event == connector.HealthEventSweep && observation.Successful {
				select {
				case observed <- struct{}{}:
				default:
				}
			}
			return err
		},
	}}, connector.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("connector.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-observed:
	case err := <-done:
		cancel()
		t.Fatalf("runtime exited before the first sweep: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the first sweep was not observed")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop")
	}

	snapshot, err := forge.LoadHealthSnapshot(dir)
	if err != nil {
		t.Fatalf("LoadHealthSnapshot: %v", err)
	}
	if len(snapshot.Connectors) != 1 || snapshot.Connectors[0].Connector != "azdo" {
		t.Fatalf("snapshot = %#v, want one azdo connector", snapshot)
	}
	health := snapshot.Connectors[0]
	if health.LastSuccessfulSweep == nil {
		t.Fatal("the completed sweep was not recorded")
	}
	if health.Subscription != forge.SubscriptionActive {
		t.Fatalf("subscription = %q, want active", health.Subscription)
	}
	if health.Status != forge.HealthDegraded || health.Reason != "no_delivery_since_sweep" {
		t.Fatalf("health = %#v, want the degraded no-delivery state", health)
	}
}

// connectorIntakeSignal records one signal the intake path sent.
type connectorIntakeSignal struct {
	WorkflowID string
	Name       string
	Arg        any
}

// fakeIntakeClient models the Temporal client surface intake uses. The embedded
// interface is never called, so a nil value is safe.
type fakeIntakeClient struct {
	client.Client
	mu            sync.Mutex
	executions    map[string]enumspb.WorkflowExecutionStatus
	starts        []client.StartWorkflowOptions
	signals       []connectorIntakeSignal
	hideExecution bool
}

func newFakeIntakeClient() *fakeIntakeClient {
	return &fakeIntakeClient{executions: map[string]enumspb.WorkflowExecutionStatus{}}
}

func (c *fakeIntakeClient) DescribeWorkflowExecution(_ context.Context, workflowID, _ string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	status, ok := c.executions[workflowID]
	if !ok || c.hideExecution {
		return nil, serviceerror.NewNotFound("workflow execution not found")
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Execution: &commonpb.WorkflowExecution{WorkflowId: workflowID},
			Status:    status,
		},
	}, nil
}

func (c *fakeIntakeClient) ExecuteWorkflow(_ context.Context, options client.StartWorkflowOptions, _ any, _ ...any) (client.WorkflowRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if status, ok := c.executions[options.ID]; ok && status == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return nil, serviceerror.NewWorkflowExecutionAlreadyStarted("workflow execution already started", options.ID, "run-existing")
	}
	c.executions[options.ID] = enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING
	c.starts = append(c.starts, options)
	return fakeIntakeRun{id: options.ID, runID: "run-1"}, nil
}

func (c *fakeIntakeClient) SignalWorkflow(_ context.Context, workflowID, _ string, signalName string, arg any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if status, ok := c.executions[workflowID]; !ok || status != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return serviceerror.NewNotFound("workflow execution not found")
	}
	c.signals = append(c.signals, connectorIntakeSignal{WorkflowID: workflowID, Name: signalName, Arg: arg})
	return nil
}

func (c *fakeIntakeClient) running(workflowID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.executions[workflowID] = enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING
}

func (c *fakeIntakeClient) complete(workflowID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.executions[workflowID] = enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED
}

func (c *fakeIntakeClient) recorded() ([]client.StartWorkflowOptions, []connectorIntakeSignal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	starts := append([]client.StartWorkflowOptions(nil), c.starts...)
	return starts, append([]connectorIntakeSignal(nil), c.signals...)
}

type fakeIntakeRun struct {
	id    string
	runID string
}

func (r fakeIntakeRun) GetID() string                  { return r.id }
func (r fakeIntakeRun) GetRunID() string               { return r.runID }
func (r fakeIntakeRun) GetFirstExecutionRunID() string { return r.runID }
func (r fakeIntakeRun) Get(context.Context, any) error { return nil }
func (r fakeIntakeRun) GetWithOptions(context.Context, any, client.WorkflowRunGetOptions) error {
	return nil
}

func newConnectorIntakeForTest(t *testing.T, temporal *fakeIntakeClient) *connectorIntake {
	t.Helper()
	return &connectorIntake{
		client:    temporal,
		taskQueue: "factory",
		dataDir:   t.TempDir(),
		logger:    slog.New(slog.DiscardHandler),
	}
}

func connectorIntakeProposal(revision string) forge.Proposal {
	return forge.Proposal{
		Candidate: forge.Candidate{
			Connector: "azdo",
			Source:    forge.SourceSweep,
			WorkItem: forge.WorkItem{
				ID:             "42",
				Revision:       revision,
				WorkItemFields: forge.WorkItemFields{Project: "Payments", Type: "Bug"},
			},
			WorkItemHash: "sha256:" + strings.Repeat("a", 64),
		},
		DeliveryID: "sweep:" + revision,
	}
}

// TestConnectorIntakeConvergesRevisionsOnOneWorkflow proves a newer revision
// signals the running execution instead of starting a second workflow.
func TestConnectorIntakeConvergesRevisionsOnOneWorkflow(t *testing.T) {
	t.Parallel()

	temporal := newFakeIntakeClient()
	intake := newConnectorIntakeForTest(t, temporal)
	if err := intake.Propose(context.Background(), connectorIntakeProposal("1")); err != nil {
		t.Fatalf("first revision: %v", err)
	}
	if err := intake.Propose(context.Background(), connectorIntakeProposal("2")); err != nil {
		t.Fatalf("second revision: %v", err)
	}

	starts, signals := temporal.recorded()
	if len(starts) != 1 {
		t.Fatalf("workflow starts = %d, want one execution for the work item", len(starts))
	}
	if len(signals) != 1 {
		t.Fatalf("revision signals = %d, want one for the newer revision", len(signals))
	}
	if signals[0].WorkflowID != starts[0].ID {
		t.Fatalf("signal went to %q, want the running execution %q", signals[0].WorkflowID, starts[0].ID)
	}
	if signals[0].Name != factory.WorkItemRevisionSignalName {
		t.Fatalf("signal name = %q, want %q", signals[0].Name, factory.WorkItemRevisionSignalName)
	}
	signal, ok := signals[0].Arg.(factory.WorkItemRevisionSignal)
	if !ok || signal.Proposal.WorkItem.Revision != "2" {
		t.Fatalf("signal payload = %#v, want the newer revision", signals[0].Arg)
	}
}

// TestConnectorIntakeStartsFreshRunAfterCompletedExecution proves a closed
// execution under the stable ID is not mistaken for a duplicate delivery.
func TestConnectorIntakeStartsFreshRunAfterCompletedExecution(t *testing.T) {
	t.Parallel()

	temporal := newFakeIntakeClient()
	intake := newConnectorIntakeForTest(t, temporal)
	if err := intake.Propose(context.Background(), connectorIntakeProposal("1")); err != nil {
		t.Fatalf("first revision: %v", err)
	}
	starts, _ := temporal.recorded()
	temporal.complete(starts[0].ID)

	if err := intake.Propose(context.Background(), connectorIntakeProposal("2")); err != nil {
		t.Fatalf("later revision: %v", err)
	}
	starts, signals := temporal.recorded()
	if len(starts) != 2 {
		t.Fatalf("workflow starts = %d, want a fresh run for the later revision", len(starts))
	}
	if starts[1].ID != starts[0].ID {
		t.Fatalf("workflow IDs = %q then %q, want one stable work-item identity", starts[0].ID, starts[1].ID)
	}
	if starts[1].WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE {
		t.Fatalf("reuse policy = %v, want ALLOW_DUPLICATE for a closed execution", starts[1].WorkflowIDReusePolicy)
	}
	if len(signals) != 0 {
		t.Fatalf("revision signals = %d, want none for a fresh run", len(signals))
	}
}

// TestConnectorIntakeSignalsLegacyRevisionWorkflow proves a delivery that
// arrives while a revision-keyed execution is still running is signalled to it
// instead of starting a second workflow.
func TestConnectorIntakeSignalsLegacyRevisionWorkflow(t *testing.T) {
	t.Parallel()

	temporal := newFakeIntakeClient()
	legacyID := factory.LegacyWorkItemWorkflowID("azdo", "42", "2")
	temporal.running(legacyID)
	intake := newConnectorIntakeForTest(t, temporal)
	if err := intake.Propose(context.Background(), connectorIntakeProposal("2")); err != nil {
		t.Fatalf("legacy revision: %v", err)
	}

	starts, signals := temporal.recorded()
	if len(starts) != 0 {
		t.Fatalf("workflow starts = %d, want none while the legacy execution runs", len(starts))
	}
	if len(signals) != 1 || signals[0].WorkflowID != legacyID {
		t.Fatalf("signals = %#v, want one to the legacy execution %q", signals, legacyID)
	}
}

// TestConnectorIntakeSignalsWhenStartRacesWithRunningWorkflow proves a start
// that loses a race to another delivery signals the winner instead of failing.
func TestConnectorIntakeSignalsWhenStartRacesWithRunningWorkflow(t *testing.T) {
	t.Parallel()

	temporal := newFakeIntakeClient()
	intake := newConnectorIntakeForTest(t, temporal)
	if err := intake.Propose(context.Background(), connectorIntakeProposal("1")); err != nil {
		t.Fatalf("first revision: %v", err)
	}
	// The lookup misses the running execution; the start reports it.
	temporal.hideExecution = true
	if err := intake.Propose(context.Background(), connectorIntakeProposal("2")); err != nil {
		t.Fatalf("racing revision: %v", err)
	}

	starts, signals := temporal.recorded()
	if len(starts) != 1 {
		t.Fatalf("workflow starts = %d, want the one race winner", len(starts))
	}
	if len(signals) != 1 {
		t.Fatalf("revision signals = %d, want the racing revision delivered", len(signals))
	}
	signal, ok := signals[0].Arg.(factory.WorkItemRevisionSignal)
	if !ok || signal.Proposal.WorkItem.Revision != "2" {
		t.Fatalf("signal payload = %#v, want the racing revision", signals[0].Arg)
	}
}

func TestBuildConnectorRuntimeRequiresWebhookSecretForListener(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "azdo.token")
	if err := os.WriteFile(tokenFile, []byte("esf:pat"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := factory.Default()
	cfg.Storage.DataDir = dir
	cfg.Connectors = map[string]factory.ConnectorConfig{
		"azdo": {
			Kind: "azuredevops", BaseURL: "http://127.0.0.1:1",
			TokenFile: tokenFile, WebhookListen: "127.0.0.1:8091",
		},
	}
	if _, err := buildConnectorRuntime(cfg, &recordingIntakeSink{}, nil); err == nil || !strings.Contains(err.Error(), "webhook_secret_file") {
		t.Fatalf("buildConnectorRuntime without an inbound credential = %v, want a webhook_secret_file error", err)
	}

	connector := cfg.Connectors["azdo"]
	connector.WebhookUsername = "esf-hooks"
	connector.WebhookSecretFile = filepath.Join(dir, "missing-webhook.secret")
	cfg.Connectors["azdo"] = connector
	if _, err := buildConnectorRuntime(cfg, &recordingIntakeSink{}, nil); err == nil || !strings.Contains(err.Error(), "webhook_secret_file") {
		t.Fatalf("buildConnectorRuntime with a missing inbound credential = %v, want a webhook_secret_file error", err)
	}
}

func TestBuildConnectorRuntimeRunsSweepOnlyWithoutWebhook(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "azdo.token")
	if err := os.WriteFile(tokenFile, []byte("esf:pat"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := factory.Default()
	cfg.Storage.DataDir = dir
	cfg.Connectors = map[string]factory.ConnectorConfig{
		"azdo": {Kind: "azuredevops", BaseURL: "http://127.0.0.1:1", TokenFile: tokenFile},
	}
	specs, err := buildConnectorRuntime(cfg, &recordingIntakeSink{}, nil)
	if err != nil {
		t.Fatalf("buildConnectorRuntime: %v", err)
	}
	if len(specs) != 1 || specs[0].WebhookHandler != nil || specs[0].WebhookListen != "" {
		t.Fatalf("sweep-only spec = %#v, want no webhook listener", specs)
	}
}
