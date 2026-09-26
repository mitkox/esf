package factory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/factoryartifacts"
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
