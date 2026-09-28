package factory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/tomlx"

	"github.com/mitkox/esf/internal/sandbox/cube"
)

func TestDefaultConfigIsValid(t *testing.T) {
	// Not parallel: it reads environment variables.
	t.Setenv("CUBE_API_URL", "http://127.0.0.1:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-test")

	cfg := Default()
	// The default configuration is deliberately NOT valid on its own: it
	// inherits the deployment's network default, which permits public egress,
	// and the factory refuses to run an agent behind an unacknowledged open
	// boundary. This test pins that contract, because a future change that
	// quietly re-opens the default would otherwise pass unnoticed.
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "acknowledge_open_egress") {
		t.Fatalf("default configuration must fail closed on unacknowledged egress, got %v", err)
	}
	cfg.Hardening.AcknowledgeOpenEgress = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("acknowledged default configuration must be valid: %v", err)
	}
	if cfg.Cube.TemplateID != "tpl-test" {
		t.Fatalf("template = %q", cfg.Cube.TemplateID)
	}
	if len(cfg.Harnesses) == 0 {
		t.Fatal("default configuration has no harness")
	}
	if len(cfg.Verification) == 0 {
		t.Fatal("default configuration has no verification profile")
	}
	if len(cfg.Sandbox.BasePackages) == 0 {
		t.Fatal("git must be a factory base package: repository preparation needs it")
	}
}

func TestProductionExampleUsesPinnedTemplateAgentWithoutUploads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "factory.toml")
	if err := os.WriteFile(path, []byte(ExampleConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sandbox.BasePackages) != 0 {
		t.Fatalf("base packages: %v", cfg.Sandbox.BasePackages)
	}
	registry, err := cfg.BuildHarnesses()
	if err != nil {
		t.Fatal(err)
	}
	h, err := registry.Resolve("opencode2")
	if err != nil {
		t.Fatal(err)
	}
	spec := h.(*agentharness.GenericCommandHarness).Spec()
	if !spec.Provision.Preinstalled || spec.Provision.BinarySource != "" || len(spec.Provision.Packages) != 0 || len(spec.PassEnv) != 0 {
		t.Fatalf("production example stages agent tools or credentials: %+v", spec)
	}
}

func TestConfigRejectsMissingCubeURL(t *testing.T) {
	t.Setenv("CUBE_API_URL", "")
	t.Setenv("E2B_API_URL", "")
	// The SDK's own default (port 3000) is deliberately NOT used: an unnoticed
	// fallback to the wrong port produces a confusing failure.
	cfg := hardenedDefaults()
	cfg.Cube.APIURL = ""
	cfg.Cube.TemplateID = "tpl-test"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected a missing API URL to be rejected")
	} else if !strings.Contains(err.Error(), "api_url") {
		t.Fatalf("error should name the missing field: %v", err)
	}
}

func TestConfigValidationProblems(t *testing.T) {
	t.Parallel()
	base := func() Config {
		cfg := Config{
			Cube:     cube.Config{APIURL: "http://127.0.0.1:4000", TemplateID: "tpl", IdleTimeout: tomlx.FromStd(time.Minute), RequestTimeout: tomlx.FromStd(time.Minute)},
			Temporal: TemporalConfig{HostPort: "127.0.0.1:7233", TaskQueue: "factory"},
			Storage:  StorageConfig{DataDir: ".factory"},
			Harnesses: map[string]HarnessConfig{
				"a": {Type: "generic", Executable: "a", Timeout: tomlx.FromStd(time.Minute)},
			},
			Verification: defaultVerificationProfiles(),
			Limits:       LimitsConfig{AgentTimeout: tomlx.FromStd(time.Minute), TotalTimeout: tomlx.FromStd(time.Hour)},
			// The base fixture is deliberately an acknowledged-open deployment:
			// the fail-closed egress rule is asserted by its own cases below.
			Hardening: HardeningConfig{AcknowledgeOpenEgress: true},
		}
		return cfg
	}

	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"no task queue", func(c *Config) { c.Temporal.TaskQueue = "" }, "task_queue"},
		{"no data dir", func(c *Config) { c.Storage.DataDir = "" }, "data_dir"},
		{"no harnesses", func(c *Config) { c.Harnesses = nil }, "harness"},
		{"harness without type", func(c *Config) { c.Harnesses = map[string]HarnessConfig{"a": {Timeout: tomlx.FromStd(time.Minute)}} }, "type is required"},
		{"harness without timeout", func(c *Config) {
			c.Harnesses = map[string]HarnessConfig{"a": {Type: "generic", Executable: "a"}}
		}, "timeout"},
		{"agent timeout exceeds total", func(c *Config) {
			c.Limits.AgentTimeout = tomlx.FromStd(2 * time.Hour)
		}, "must not exceed"},
		{"unacknowledged open deployment default", func(c *Config) {
			c.Hardening.AcknowledgeOpenEgress = false
			c.Egress = nil
		}, "acknowledge_open_egress"},
		{"unacknowledged open policy", func(c *Config) {
			c.Hardening.AcknowledgeOpenEgress = false
			c.Egress = map[string]EgressPolicyConfig{"open": {AllowInternet: boolPtr(true)}}
		}, "acknowledge_open_egress"},
		{"unacknowledged nil-internet policy", func(c *Config) {
			c.Hardening.AcknowledgeOpenEgress = false
			c.Egress = map[string]EgressPolicyConfig{"implicit": {}}
		}, "acknowledge_open_egress"},
		{"explicit deny needs no acknowledgement", func(c *Config) {
			c.Hardening.AcknowledgeOpenEgress = false
			c.Egress = map[string]EgressPolicyConfig{"strict": {AllowInternet: boolPtr(false)}}
		}, ""},
		{"invalid trip severity", func(c *Config) {
			c.Hardening.TripSeverity = "APOCALYPTIC"
		}, "trip_severity"},
		{"canary requires https", func(c *Config) {
			c.Hardening.EgressCanaryURL = "http://example.com/"
		}, "egress_canary_url"},
		{"webhook rejects embedded credentials", func(c *Config) {
			c.Hardening.AlertWebhookURL = "https://user:pass@hooks.example.com/x"
		}, "alert_webhook_url"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := base()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestBuildHarnessesRejectsUnknownType(t *testing.T) {
	t.Parallel()
	cfg := hardenedDefaults()
	cfg.Harnesses = map[string]HarnessConfig{
		"mystery": {Type: "quantum", Timeout: tomlx.FromStd(time.Minute)},
	}
	if _, err := cfg.BuildHarnesses(); err == nil {
		t.Fatal("expected an unknown harness type to be rejected")
	}
}

func TestBuildHarnessesRegistersConfiguredNames(t *testing.T) {
	t.Parallel()
	cfg := hardenedDefaults()
	registry, err := cfg.BuildHarnesses()
	if err != nil {
		t.Fatalf("BuildHarnesses: %v", err)
	}
	for _, name := range registry.Names() {
		if _, err := registry.Resolve(name); err != nil {
			t.Fatalf("registered harness %q is not resolvable: %v", name, err)
		}
	}
	// A caller cannot invent a harness.
	if _, err := registry.Resolve("not-configured"); err == nil {
		t.Fatal("an unconfigured harness name must not resolve")
	}
}

func TestBuildHarnessesCreatesUnrealHarness(t *testing.T) {
	t.Parallel()
	cfg := hardenedDefaults()
	cfg.Harnesses = map[string]HarnessConfig{
		"unreal": {
			Type: "unreal", Binary: "/host/unreal-agent-runner",
			BinarySHA256: strings.Repeat("a", 64),
			Provider:     "openrouter", BaseURL: "https://openrouter.ai/api/v1",
			Model: "vendor/model", APIKeyEnv: "OPENROUTER_API_KEY",
			ThinkingLevel: "high", Timeout: tomlx.FromStd(time.Minute),
		},
	}
	registry, err := cfg.BuildHarnesses()
	if err != nil {
		t.Fatalf("BuildHarnesses: %v", err)
	}
	harness, err := registry.Resolve("unreal")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	generic, ok := harness.(*agentharness.GenericCommandHarness)
	if !ok {
		t.Fatalf("harness type = %T, want *agentharness.GenericCommandHarness", harness)
	}
	if got := generic.Spec().Executable; got != agentharness.UnrealBinaryPath {
		t.Fatalf("unreal executable = %q", got)
	}
}

func TestBuildHarnessesRejectsUnrealPassEnv(t *testing.T) {
	t.Parallel()
	cfg := hardenedDefaults()
	cfg.Harnesses = map[string]HarnessConfig{
		"unreal": {
			Type: "unreal", Binary: "/host/runner", Provider: "openai",
			BinarySHA256: strings.Repeat("a", 64),
			Model:        "model", APIKeyEnv: "OPENAI_API_KEY",
			PassEnv: []string{"HOME"}, Timeout: tomlx.FromStd(time.Minute),
		},
	}
	if _, err := cfg.BuildHarnesses(); err == nil || !strings.Contains(err.Error(), "does not accept pass_env") {
		t.Fatalf("BuildHarnesses error = %v", err)
	}
}

func TestBuildHarnessesRejectsCustomUnrealInvocation(t *testing.T) {
	t.Parallel()
	cfg := hardenedDefaults()
	cfg.Harnesses = map[string]HarnessConfig{
		"unreal": {
			Type: "unreal", Binary: "/host/runner", Provider: "openai",
			BinarySHA256: strings.Repeat("a", 64),
			Model:        "model", APIKeyEnv: "OPENAI_API_KEY", Executable: "/bin/sh",
			Timeout: tomlx.FromStd(time.Minute),
		},
	}
	if _, err := cfg.BuildHarnesses(); err == nil || !strings.Contains(err.Error(), "invocation is fixed") {
		t.Fatalf("BuildHarnesses error = %v", err)
	}
}

func TestLoadConfigFile(t *testing.T) {
	t.Setenv("CUBE_API_URL", "http://127.0.0.1:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-test")

	dir := t.TempDir()
	path := filepath.Join(dir, "factory.toml")
	body := `
factory_version = "9.9.9"

[cube]
api_url = "http://127.0.0.1:4000"
template_id = "tpl-from-file"

[temporal]
host_port = "127.0.0.1:7233"
task_queue = "custom-queue"

[storage]
data_dir = "` + dir + `/.factory"

[sandbox]
base_packages = ["git"]

[hardening]
# This fixture exercises config loading, not egress posture; the fail-closed
# egress rule has its own cases in TestConfigValidationProblems.
acknowledge_open_egress = true

[harnesses.agent]
type = "generic"
executable = "/bin/echo"
timeout = "2m"

[verification.default]
name = "default"

[[verification.default.steps]]
id = "build"
argv = ["./build.sh"]
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.FactoryVersion != "9.9.9" {
		t.Fatalf("factory_version = %q", cfg.FactoryVersion)
	}
	if cfg.Cube.TemplateID != "tpl-from-file" {
		t.Fatalf("template = %q", cfg.Cube.TemplateID)
	}
	if cfg.Temporal.TaskQueue != "custom-queue" {
		t.Fatalf("task_queue = %q", cfg.Temporal.TaskQueue)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loaded config is invalid: %v", err)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "factory.toml")
	if err := os.WriteFile(path, []byte("surprise_option = true\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// An unknown key is almost always a typo that would otherwise silently
	// disable a setting.
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected an unknown configuration key to be rejected")
	}
}

func TestEffectiveVersion(t *testing.T) {
	t.Parallel()
	if got := (Config{}).effectiveVersion(); got != Version {
		t.Fatalf("effectiveVersion() = %q, want %q", got, Version)
	}
	if got := (Config{FactoryVersion: "custom"}).effectiveVersion(); got != "custom" {
		t.Fatalf("effectiveVersion() = %q, want custom", got)
	}
}

func TestWorkflowIDRoundTrip(t *testing.T) {
	t.Parallel()
	id := WorkflowIDForRun("run-abc")
	if id != "factory-run-run-abc" {
		t.Fatalf("WorkflowIDForRun = %q", id)
	}
	runID, ok := RunIDFromWorkflowID(id)
	if !ok || runID != "run-abc" {
		t.Fatalf("RunIDFromWorkflowID = %q, %v", runID, ok)
	}
	if _, ok := RunIDFromWorkflowID("unrelated"); ok {
		t.Fatal("a foreign workflow id must not decode")
	}
}

// hardenedDefaults returns the development defaults with the open-egress risk
// acknowledged.
//
// Test fixtures care about the behavior under test, not the network posture;
// the fail-closed egress rule has its own dedicated cases in
// TestConfigValidationProblems and TestDefaultConfigIsValid.
//
// The per-run egress probe is disabled here because several fixtures use shim
// sandboxes that execute commands on the HOST, where the probe would make real
// network calls. The probe has dedicated tests in egress_probe_test.go.
func hardenedDefaults() Config {
	cfg := Default()
	cfg.Hardening.AcknowledgeOpenEgress = true
	cfg.Hardening.EgressProbe = boolPtr(false)
	return cfg
}
