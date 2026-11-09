package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/notify"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/verification"
)

type failingEvidenceFactory struct {
	artifacts.Factory
	fail string
}
type failingEvidenceStore struct {
	artifacts.Store
	fail string
}

func (f failingEvidenceFactory) ForRun(id string) (artifacts.Store, error) {
	s, err := f.Factory.ForRun(id)
	return failingEvidenceStore{s, f.fail}, err
}
func (s failingEvidenceStore) Write(path string, data []byte) error {
	if path == s.fail {
		return fmt.Errorf("disk full writing %s", path)
	}
	return s.Store.Write(path, data)
}

func TestActivitiesSurfaceEvidenceWriteFailures(t *testing.T) {
	for _, path := range []string{ArtifactAgentStdout, ArtifactAgentStderr, ArtifactAgentPrompt, ArtifactAgentResult, "verification/check.stdout", "verification/check.stderr", ArtifactVerificationResult} {
		t.Run(path, func(t *testing.T) {
			provider := sandbox.NewFake()
			sb, err := provider.Create(context.Background(), sandbox.Spec{})
			if err != nil {
				t.Fatal(err)
			}
			store, err := artifacts.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			harness, err := agentharness.NewGeneric(agentharness.Spec{Name: "test", Executable: "agent", Timeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			registry := agentharness.NewRegistry()
			if err := registry.Register(harness); err != nil {
				t.Fatal(err)
			}
			acts, err := NewActivities(ActivitiesOptions{
				Provider: provider, Repositories: repository.New(nil), Harnesses: registry,
				ArtifactFactory: failingEvidenceFactory{store, path}, Redactor: NewRedactor(),
				Profiles: verification.Profiles{Profiles: map[string]verification.Profile{"test": {Name: "test", Steps: []verification.Step{{ID: "check", Argv: []string{"true"}}}}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var evidence string
			if strings.HasPrefix(path, "verification") {
				out, err := acts.RunVerification(context.Background(), RunVerificationInput{RunID: "run-test", SandboxID: sb.ID(), Profile: "test", RepositoryDir: "/repo"})
				if err != nil {
					t.Fatalf("must not trigger execution retry: %v", err)
				}
				evidence = out.EvidenceError
			} else {
				out, err := acts.RunAgent(context.Background(), RunAgentInput{RunID: "run-test", SandboxID: sb.ID(), Harness: "test", RepositoryDir: "/repo", Prompt: "task", Timeout: time.Minute})
				if err != nil {
					t.Fatalf("must not trigger execution retry: %v", err)
				}
				evidence = out.EvidenceError
			}
			if !strings.Contains(evidence, "disk full") {
				t.Fatalf("storage error lost: %q", evidence)
			}
		})
	}
}

func TestRecordEvidenceFailureCountsAndAlerts(t *testing.T) {
	var mu sync.Mutex
	var received notify.Event
	webhook := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			t.Errorf("decode alert: %v", err)
		}
		mu.Lock()
		received = event
		mu.Unlock()
		response.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	runStore, err := store.ForRun("run-evidence")
	if err != nil {
		t.Fatalf("ForRun: %v", err)
	}
	manifest := RunManifest{RunID: "run-evidence", ChangeID: "change-evidence", FactoryResult: StateSucceeded}
	if err := runStore.Write(ArtifactRun, []byte(`{"run_id":"run-evidence","change_id":"change-evidence","factory_result":"SUCCEEDED"}`)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	metrics, reader := newManualMetrics(t)
	acts := &Activities{
		artifactFactory: store,
		redactor:        NewRedactor(),
		telemetry:       &Telemetry{metrics: metrics},
		cfg:             Config{Hardening: HardeningConfig{AlertWebhookURL: webhook.URL}},
		log:             slog.New(slog.DiscardHandler),
	}
	out, err := acts.RecordEvidenceFailure(context.Background(), RecordEvidenceFailureInput{
		RunID: "run-evidence", Error: "signing key is unreadable",
	})
	if err != nil {
		t.Fatalf("RecordEvidenceFailure: %v", err)
	}
	if out.Manifest.FactoryResult != StateEvidenceFailed || out.Manifest.ChangeID != manifest.ChangeID {
		t.Fatalf("manifest = %+v", out.Manifest)
	}
	if out.Manifest.Hardening == nil || len(out.Manifest.Hardening.Alerts) != 1 ||
		out.Manifest.Hardening.Alerts[0].Kind != AlertEvidenceFailed {
		t.Fatalf("alerts = %+v", out.Manifest.Hardening)
	}
	mu.Lock()
	defer mu.Unlock()
	if received.Kind != AlertEvidenceFailed || received.Detail["change"] != "change-evidence" {
		t.Fatalf("alert = %+v", received)
	}
	collected := collectMetrics(t, reader)
	if got := sumValueFor(t, metricByName(t, collected, "factory_evidence_failures_total").Data, AttrFailureClass, "signing_failed"); got != 1 {
		t.Fatalf("evidence failures = %d, want 1", got)
	}
}
