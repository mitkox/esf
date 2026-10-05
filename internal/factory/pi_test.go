package factory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

func piFactoryConfig(t *testing.T) Config {
	t.Helper()
	cfg := testValidConfigForCredentialPolicy()
	cache := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(cache, []byte(`{"id":"test-model","api":"openai-completions","input":["text"],"contextWindow":32768,"maxTokens":1024,"thinkingLevels":["off"],"pricingKnown":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Harnesses["pi"] = HarnessConfig{Type: "pi", Binary: "/opt/esf/agents/pi-runner.mjs", BinarySHA256: strings.Repeat("a", 64), RuntimeSHA256: strings.Repeat("b", 64), Preinstalled: true, Provider: "local", BaseURL: "http://gateway.internal:8000/v1", Model: "test-model", CatalogCache: cache, Timeout: tomlx.FromStd(time.Minute)}
	return cfg
}

func TestPiConfigurationRegistersWithoutChangingDefault(t *testing.T) {
	cfg := piFactoryConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	reg, err := cfg.BuildHarnesses()
	if err != nil {
		t.Fatal(err)
	}
	h, err := reg.Resolve("pi")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.(*agentharness.PiHarness); !ok {
		t.Fatalf("harness=%T", h)
	}
	for _, mutate := range []func(*HarnessConfig){func(h *HarnessConfig) { h.Args = []string{"evil"} }, func(h *HarnessConfig) { h.PassEnv = []string{"SECRET"} }, func(h *HarnessConfig) { h.RuntimeSHA256 = "" }, func(h *HarnessConfig) { h.CatalogCache = "missing.json" }} {
		bad := piFactoryConfig(t)
		h := bad.Harnesses["pi"]
		mutate(&h)
		bad.Harnesses["pi"] = h
		if bad.Validate() == nil {
			t.Fatal("unsafe Pi configuration accepted")
		}
	}
}

func TestPiRejectsResolvedEndpointBeforeSandboxAllocation(t *testing.T) {
	cfg := piFactoryConfig(t)
	cfg.Repositories.Allowed = []string{"https://github.com/acme/"}
	cfg.Models = map[string]ModelConfig{"other": {Provider: "local", Model: "test-model", BaseURL: "http://wrong.internal:8000/v1"}}
	reg, err := cfg.BuildHarnesses()
	if err != nil {
		t.Fatal(err)
	}
	provider := sandbox.NewFake()
	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acts, err := NewActivities(ActivitiesOptions{Config: cfg, Provider: provider, Repositories: repository.New(cfg.Repositories.Allowed), Harnesses: reg, Profiles: cfg.VerificationProfiles(), ArtifactFactory: store})
	if err != nil {
		t.Fatal(err)
	}
	_, err = acts.ValidateRequest(context.Background(), ValidateInput{Request: RunRequest{RunID: "run-pi", Repository: "https://github.com/acme/repo", Revision: "abc", Task: "fix", AgentHarness: "pi", VerificationProfile: "default", Model: "other"}})
	if err == nil || !strings.Contains(err.Error(), "resolved model endpoint") {
		t.Fatalf("error=%v", err)
	}
	if len(provider.Created()) != 0 {
		t.Fatal("allocated sandbox before model policy rejection")
	}
}

type piEvidenceHarness struct{ agentharness.Harness }

func (h piEvidenceHarness) Run(context.Context, sandbox.Sandbox, agentharness.Task) (agentharness.Result, error) {
	return agentharness.Result{ExitCode: 0, Evidence: map[string][]byte{"agent/usage.json": []byte(`{"secret":"provider-secret"}`), "../../escape": []byte("unsafe")}}, nil
}
func TestPiSupplementaryEvidenceIsRedactedAndTransient(t *testing.T) {
	provider := sandbox.NewFake()
	sb, _ := provider.Create(context.Background(), sandbox.Spec{})
	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	generic, _ := agentharness.NewGeneric(agentharness.Spec{Name: "pi", Executable: "agent", Timeout: time.Minute})
	reg := agentharness.NewRegistry()
	_ = reg.Register(piEvidenceHarness{generic})
	acts, err := NewActivities(ActivitiesOptions{Provider: provider, Repositories: repository.New(nil), Harnesses: reg, ArtifactFactory: store, Redactor: NewRedactor("provider-secret")})
	if err != nil {
		t.Fatal(err)
	}
	out, err := acts.RunAgent(context.Background(), RunAgentInput{RunID: "pi-evidence", SandboxID: sb.ID(), Harness: "pi", Prompt: "task", RepositoryDir: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	runStore, _ := store.ForRun("pi-evidence")
	bytes, err := runStore.Read("agent/usage.json")
	if err != nil || strings.Contains(string(bytes), "provider-secret") {
		t.Fatalf("evidence=%s err=%v", bytes, err)
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), "provider-secret") || strings.Contains(string(encoded), "escape") {
		t.Fatal("transient evidence leaked into workflow payload")
	}
	if out.EvidenceError == "" {
		t.Fatal("unsafe evidence path not surfaced")
	}
}

func TestPiCredentialRemainsAtCubeEgress(t *testing.T) {
	cfg := piFactoryConfig(t)
	h := cfg.Harnesses["pi"]
	h.Provider = "openai"
	h.BaseURL = "https://api.openai.com/v1"
	h.APIKeyEnv = "TEST_PI_KEY"
	h.CredentialMode = "cube_egress"
	cfg.Harnesses["pi"] = h
	t.Setenv("TEST_PI_KEY", "test-provider-secret")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	network, applies, err := cfg.runtimeNetworkForHarness("pi")
	if err != nil || !applies {
		t.Fatalf("policy=%v %v", applies, err)
	}
	if *network.AllowInternet || len(network.Rules) != 1 || network.Rules[0].Action.Inject[0].Secret != "test-provider-secret" {
		t.Fatal("Pi credential policy not scoped to proxy")
	}
}
