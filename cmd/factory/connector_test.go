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
	cfg := factory.Default()
	cfg.Storage.DataDir = dir
	cfg.Connectors = map[string]factory.ConnectorConfig{
		"azdo": {
			Kind:          "azuredevops",
			BaseURL:       "http://127.0.0.1:1",
			TokenFile:     tokenFile,
			WebhookListen: "127.0.0.1:8091",
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

	oversized := bytes.Repeat([]byte("x"), 1<<20+1)
	request, err = http.NewRequest(http.MethodPost, server.URL+"/hooks/azuredevops", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("esf", "pat")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("oversized POST: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", response.StatusCode)
	}
	if stats := webhook.Stats(); stats.Unauthorized != 1 {
		t.Fatalf("stats = %#v, want one authentication rejection", stats)
	}
	if proposals, rejections := sink.counts(); proposals != 0 || rejections != 0 {
		t.Fatalf("proposals = %d, rejections = %d, want none", proposals, rejections)
	}
}

type connectorSweeperFunc func(context.Context) ([]forge.Candidate, error)

func (f connectorSweeperFunc) Sweep(ctx context.Context) ([]forge.Candidate, error) { return f(ctx) }

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
	reporter, err := factory.NewConnectorHealthReporter(artifactFactory, telemetry, nil)
	if err != nil {
		t.Fatalf("NewConnectorHealthReporter: %v", err)
	}
	observe := connectorHealthObserver(reporter)
	observed := make(chan struct{}, 1)
	runtime, err := connector.New([]connector.Spec{{
		Name:          "azdo",
		SweepInterval: time.Hour,
		Sweeper: connectorSweeperFunc(func(context.Context) ([]forge.Candidate, error) {
			return nil, nil
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
