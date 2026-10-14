package factory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

func TestNativeCLIConfigurationAndEgress(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			cfg := testValidConfigForCredentialPolicy()
			baseURL, header, format := "https://api.openai.com/v1", "Authorization", "Bearer ${SECRET}"
			if kind == "claude" {
				baseURL, header, format = "https://api.anthropic.com", "x-api-key", "${SECRET}"
			}
			hc := HarnessConfig{Type: kind, Binary: "/opt/esf/agents/" + kind, BinarySHA256: strings.Repeat("a", 64), Preinstalled: true, BaseURL: baseURL, APIKeyEnv: "TEST_NATIVE_KEY", CredentialMode: "cube_egress", Timeout: tomlx.FromStd(time.Minute)}
			cfg.Harnesses[kind] = hc
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			registry, err := cfg.BuildHarnesses()
			if err != nil {
				t.Fatal(err)
			}
			h, err := registry.Resolve(kind)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := h.(*agentharness.NativeCLIHarness); !ok {
				t.Fatalf("harness %T", h)
			}
			if _, err := registry.Resolve(agentharness.OpenCodeHarnessName); err != nil {
				t.Fatal("default harness removed")
			}
			t.Setenv("TEST_NATIVE_KEY", "test-provider-secret")
			network, applies, err := cfg.runtimeNetworkForHarness(kind)
			if err != nil || !applies || *network.AllowInternet {
				t.Fatalf("policy=%+v applies=%v err=%v", network, applies, err)
			}
			inject := network.Rules[0].Action.Inject[0]
			if inject.Header != header || inject.Format != format || inject.Secret != "test-provider-secret" {
				t.Fatalf("injection=%+v", inject)
			}
			paths := []string{"/v1/responses", "/v1/responses/compact"}
			if kind == "claude" {
				paths = []string{"/v1/messages", "/v1/messages/count_tokens"}
			}
			if len(network.Rules) != len(paths) {
				t.Fatal("unexpected credential routes")
			}
			for i, rule := range network.Rules {
				if rule.Match.Path != paths[i] || rule.Match.Scheme != "https" || len(rule.Match.Method) != 1 || rule.Match.Method[0] != "POST" || rule.Match.Host != rule.Match.SNI {
					t.Fatalf("credential injection is too broad: %+v", rule.Match)
				}
			}
			for _, mutate := range []func(*HarnessConfig){
				func(h *HarnessConfig) { h.Args = []string{"custom"} },
				func(h *HarnessConfig) { h.PassEnv = []string{"SECRET"} },
				func(h *HarnessConfig) { h.ProviderFiles = map[string]string{"/root/auth": "auth"} },
				func(h *HarnessConfig) { h.Provider = "incompatible" },
				func(h *HarnessConfig) { h.BinarySHA256 = "not-a-digest" },
				func(h *HarnessConfig) { h.CredentialMode = "environment" },
				func(h *HarnessConfig) { h.CredentialMode = "" },
			} {
				bad := hc
				mutate(&bad)
				cfg.Harnesses[kind] = bad
				if cfg.Validate() == nil {
					t.Fatalf("unsafe configuration accepted: %+v", bad)
				}
			}
		})
	}
}

func TestNativeCLIActivityKeepsSecretsOutOfHistoryAndEvidence(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			const secret = "synthetic-native-secret-stays-in-proxy"
			t.Setenv("TEST_NATIVE_KEY", secret)
			cfg := testValidConfigForCredentialPolicy()
			base := "https://api.openai.com/v1"
			if kind == "claude" {
				base = "https://api.anthropic.com"
			}
			cfg.Harnesses[kind] = HarnessConfig{Type: kind, BaseURL: base, APIKeyEnv: "TEST_NATIVE_KEY", CredentialMode: "cube_egress", Timeout: tomlx.FromStd(time.Minute)}
			reg, err := cfg.BuildHarnesses()
			if err != nil {
				t.Fatal(err)
			}
			fake := sandbox.NewFake()
			sb, _ := fake.Create(ctx, sandbox.Spec{})
			h, _ := reg.Resolve(kind)
			if err := h.(agentharness.ModelEndpointConfigurer).ConfigureModelEndpoint(ctx, sb, agentharness.ModelEndpoint{}); err != nil {
				t.Fatal(err)
			}
			fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
				encoded, _ := json.Marshal(cmd)
				if strings.Contains(string(encoded), secret) {
					t.Fatal("secret reached the VM command")
				}
				terminal := `{"type":"turn.completed","debug":"` + secret + `"}`
				if kind == "claude" {
					terminal = `{"type":"result","subtype":"success","debug":"` + secret + `"}`
				}
				return sandbox.Execution{Stdout: terminal}, nil
			}
			store, err := artifacts.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			acts, err := NewActivities(ActivitiesOptions{Config: cfg, Provider: fake, Harnesses: reg, Repositories: repository.New(nil), ArtifactFactory: store, Redactor: NewRedactor(collectSecrets(cfg, reg)...)})
			if err != nil {
				t.Fatal(err)
			}
			out, err := acts.RunAgent(ctx, RunAgentInput{RunID: "native-secret-check", SandboxID: sb.ID(), Harness: kind, Prompt: "fix", RepositoryDir: "/repo", ModelAPIKeyEnv: "TEST_NATIVE_KEY", Env: map[string]string{"OTHER_TOKEN": secret}})
			if err != nil || out.Result.ExitCode != 0 {
				t.Fatalf("result=%+v err=%v", out, err)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), secret) {
				t.Fatal("secret entered workflow history")
			}
			runStore, _ := store.ForRun("native-secret-check")
			for _, path := range []string{ArtifactAgentStdout, ArtifactAgentStderr, ArtifactAgentResult, ArtifactAgentPrompt} {
				data, err := runStore.Read(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), secret) {
					t.Fatal("secret entered durable evidence")
				}
			}
		})
	}
}

type rejectingNativeNetworkSandbox struct {
	sandbox.Sandbox
	secret string
}

func (s rejectingNativeNetworkSandbox) UpdateNetwork(context.Context, sandbox.Network) error {
	return errors.New("policy rejected: " + s.secret)
}

type rejectingNativeNetworkProvider struct {
	sandbox.Provider
	secret string
}

func (p rejectingNativeNetworkProvider) Reattach(ctx context.Context, id string) (sandbox.Sandbox, error) {
	sb, err := p.Provider.Reattach(ctx, id)
	if err != nil {
		return nil, err
	}
	return rejectingNativeNetworkSandbox{Sandbox: sb, secret: p.secret}, nil
}
func TestRuntimeNetworkRedactsRotatedSecretFromProviderErrors(t *testing.T) {
	ctx := context.Background()
	const secret = "rotated-native-credential-must-be-redacted"
	t.Setenv("TEST_NATIVE_KEY", secret)
	cfg := testValidConfigForCredentialPolicy()
	cfg.Harnesses["codex"] = HarnessConfig{Type: "codex", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "TEST_NATIVE_KEY", CredentialMode: "cube_egress", Timeout: tomlx.FromStd(time.Minute)}
	reg, err := cfg.BuildHarnesses()
	if err != nil {
		t.Fatal(err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	store, _ := artifacts.NewLocal(t.TempDir())
	acts, err := NewActivities(ActivitiesOptions{Config: cfg, Provider: rejectingNativeNetworkProvider{Provider: fake, secret: secret}, Harnesses: reg, Repositories: repository.New(nil), ArtifactFactory: store, Redactor: NewRedactor("old-key-before-rotation")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = acts.ApplyRuntimeNetwork(ctx, ApplyRuntimeNetworkInput{RunID: "native-policy-error", SandboxID: sb.ID(), Harness: "codex"})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), RedactionMask) {
		t.Fatalf("unsafe policy error: %v", err)
	}
}
