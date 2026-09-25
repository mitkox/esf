package factory

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

func TestRuntimeNetworkForUnrealKeepsCredentialAtEgress(t *testing.T) {
	t.Setenv("TEST_MODEL_KEY", "provider-secret")
	cfg := Config{
		Sandbox: SandboxConfig{RuntimeAllowOut: []string{"proxy.golang.org"}},
		Harnesses: map[string]HarnessConfig{
			"unreal": {
				Type: "unreal", Provider: "openrouter",
				BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "TEST_MODEL_KEY",
				CredentialMode: cubeEgressCredentialMode,
			},
		},
	}
	network, applies, err := cfg.runtimeNetworkForHarness("unreal")
	if err != nil {
		t.Fatalf("runtimeNetworkForHarness: %v", err)
	}
	if !applies || network.AllowInternet == nil || *network.AllowInternet {
		t.Fatalf("network is not default-deny: %+v", network)
	}
	if len(network.AllowOut) != 1 || network.AllowOut[0] != "proxy.golang.org" {
		t.Fatalf("allow out = %v", network.AllowOut)
	}
	if len(network.Rules) != 1 {
		t.Fatalf("rules = %d", len(network.Rules))
	}
	rule := network.Rules[0]
	if rule.Match.Host != "openrouter.ai" || rule.Match.SNI != "openrouter.ai" || rule.Match.Scheme != "https" || rule.Match.Path != "/api/v1/*" {
		t.Fatalf("match = %+v", rule.Match)
	}
	if len(rule.Action.Inject) != 1 || rule.Action.Inject[0].Secret != "provider-secret" || rule.Action.Inject[0].Format != "Bearer ${SECRET}" {
		t.Fatalf("inject = %+v", rule.Action.Inject)
	}
}

func TestRuntimeNetworkReadsOwnerOnlyCredentialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider-key")
	if err := os.WriteFile(path, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Harnesses: map[string]HarnessConfig{
		"unreal": {
			Type: "unreal", Provider: "openai", BaseURL: "https://api.openai.com/v1",
			APIKeyFile: path, CredentialMode: cubeEgressCredentialMode,
		},
	}}
	network, applies, err := cfg.runtimeNetworkForHarness("unreal")
	if err != nil {
		t.Fatalf("runtimeNetworkForHarness: %v", err)
	}
	if !applies || network.Rules[0].Action.Inject[0].Secret != "file-secret" {
		t.Fatalf("credential file was not resolved")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cfg.runtimeNetworkForHarness("unreal"); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("world-readable credential accepted: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "provider-key-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	cfg.Harnesses["unreal"] = HarnessConfig{
		Type: "unreal", Provider: "openai", BaseURL: "https://api.openai.com/v1",
		APIKeyFile: link, CredentialMode: cubeEgressCredentialMode,
	}
	if _, _, err := cfg.runtimeNetworkForHarness("unreal"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink credential accepted: %v", err)
	}
}

func TestApplyRuntimeNetworkUsesProviderUpdater(t *testing.T) {
	t.Setenv("TEST_MODEL_KEY", "provider-secret")
	provider := sandbox.NewFake()
	sb, err := provider.Create(context.Background(), sandbox.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	telemetry, err := NewTelemetry(context.Background(), ObservabilityConfig{})
	if err != nil {
		t.Fatalf("NewTelemetry: %v", err)
	}
	acts := &Activities{
		provider: provider,
		cfg: Config{Harnesses: map[string]HarnessConfig{
			"unreal": {
				Type: "unreal", Provider: "openai", BaseURL: "https://api.openai.com/v1",
				APIKeyEnv: "TEST_MODEL_KEY", CredentialMode: cubeEgressCredentialMode,
				Timeout: tomlx.FromStd(time.Minute),
			},
		}},
		telemetry: telemetry,
		log:       slog.New(slog.DiscardHandler),
	}
	out, err := acts.ApplyRuntimeNetwork(context.Background(), ApplyRuntimeNetworkInput{
		RunID: "run", SandboxID: sb.ID(), Harness: "unreal",
	})
	if err != nil {
		t.Fatalf("ApplyRuntimeNetwork: %v", err)
	}
	if !out.Applied || len(provider.Networks(sb.ID())) != 1 {
		t.Fatalf("output=%+v networks=%v", out, provider.Networks(sb.ID()))
	}
}

func TestCubeEgressConfigRejectsUnsafeEndpoint(t *testing.T) {
	cfg := testValidConfigForCredentialPolicy()
	cfg.Harnesses["unreal"] = HarnessConfig{
		Type: "unreal", Provider: "openai", BaseURL: "http://api.openai.com/v1",
		APIKeyEnv: "OPENAI_API_KEY", CredentialMode: cubeEgressCredentialMode,
		Binary: "/host/runner", BinarySHA256: strings.Repeat("a", 64), Model: "model",
		Timeout: tomlx.FromStd(time.Minute),
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires an https base_url") {
		t.Fatalf("Validate error = %v", err)
	}
}

func TestCubeEgressConfigRejectsPlaintextRemoteControlPlane(t *testing.T) {
	cfg := testValidConfigForCredentialPolicy()
	cfg.Cube.APIURL = "http://cube.example.com:4000"
	cfg.Harnesses["unreal"] = HarnessConfig{
		Type: "unreal", Provider: "openai", BaseURL: "https://api.openai.com/v1",
		APIKeyEnv: "OPENAI_API_KEY", CredentialMode: cubeEgressCredentialMode,
		Binary: "/host/runner", BinarySHA256: strings.Repeat("a", 64), Model: "model",
		Timeout: tomlx.FromStd(time.Minute),
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "HTTPS or loopback") {
		t.Fatalf("Validate error = %v", err)
	}
}

func testValidConfigForCredentialPolicy() Config {
	cfg := Default()
	cfg.Cube.APIURL = "http://127.0.0.1:4000"
	cfg.Cube.TemplateID = "tpl"
	return cfg
}
