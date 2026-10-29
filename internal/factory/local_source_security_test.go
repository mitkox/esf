package factory

import (
	"context"
	"errors"
	"testing"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
)

func TestDirectActivityRejectsUnregisteredHostRepository(t *testing.T) {
	cfg := piFactoryConfig(t)
	reg, err := cfg.BuildHarnesses()
	if err != nil {
		t.Fatal(err)
	}
	provider := sandbox.NewFake()
	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acts, err := NewActivities(ActivitiesOptions{Config: cfg, Provider: provider, Repositories: repository.New(nil), Harnesses: reg, Profiles: cfg.VerificationProfiles(), ArtifactFactory: store})
	if err != nil {
		t.Fatal(err)
	}
	req := RunRequest{RunID: "run-local-policy", RepositoryKind: "local", LocalPath: t.TempDir(), Revision: "abc", Task: "inspect", AgentHarness: "pi", VerificationProfile: "default"}
	if _, err := acts.ValidateRequest(context.Background(), ValidateInput{Request: req}); !errors.Is(err, repository.ErrRepositoryRejected) {
		t.Fatalf("direct admission: %v", err)
	}
	if len(provider.Created()) != 0 {
		t.Fatal("allocated sandbox before policy rejection")
	}
	sb, err := provider.Create(context.Background(), sandbox.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acts.PrepareRepository(context.Background(), PrepareRepositoryInput{Request: req, SandboxID: sb.ID()}); !errors.Is(err, repository.ErrRepositoryRejected) {
		t.Fatalf("direct preparation: %v", err)
	}
}
