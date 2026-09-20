package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/tomlx"
)

// These tests cover the `factory apply` manifest surface. The properties that
// matter are security properties: a manifest cannot express anything the CLI
// flags cannot, a typo must fail loudly, and validation happens before a
// workflow is started.

func writeSpec(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func specConfig() factory.Config {
	cfg := factory.Default()
	cfg.Cube.APIURL = "http://127.0.0.1:4000"
	cfg.Cube.TemplateID = "tpl-default"
	cfg.Limits.AgentTimeout = tomlx.FromStd(30 * 60 * 1e9)
	cfg.Limits.VerificationTimeout = tomlx.FromStd(15 * 60 * 1e9)
	cfg.Limits.TotalTimeout = tomlx.FromStd(90 * 60 * 1e9)
	cfg.Models = map[string]factory.ModelConfig{
		"fast": {Provider: "anthropic", Model: "claude-fast", APIKeyEnv: "ANTHROPIC_API_KEY"},
	}
	return cfg
}

func TestReadRunSpecAcceptsTOMLAndJSON(t *testing.T) {
	tomlPath := writeSpec(t, "run.toml", `
run_id = "run-1"
repository = "https://github.com/acme/x"
revision = "abc"
task = "fix it"
agent_harness = "opencode2"
`)
	spec, err := readRunSpec(tomlPath)
	if err != nil {
		t.Fatalf("readRunSpec(toml): %v", err)
	}
	if spec.RunID != "run-1" || spec.Repository != "https://github.com/acme/x" {
		t.Fatalf("toml spec = %+v", spec)
	}

	jsonPath := writeSpec(t, "run.json", `{
  "run_id": "run-2",
  "repository": "https://github.com/acme/x",
  "revision": "abc",
  "task": "fix it",
  "scope": "payments"
}`)
	spec, err = readRunSpec(jsonPath)
	if err != nil {
		t.Fatalf("readRunSpec(json): %v", err)
	}
	if spec.RunID != "run-2" || spec.Scope != "payments" {
		t.Fatalf("json spec = %+v", spec)
	}
}

// TestReadRunSpecRejectsUnknownFields proves a typo in a reviewed manifest fails
// instead of silently dropping the setting it was meant to change.
func TestReadRunSpecRejectsUnknownFields(t *testing.T) {
	path := writeSpec(t, "run.toml", `
run_id = "run-1"
repository = "https://github.com/acme/x"
revision = "abc"
task = "fix it"
agentharness = "opencode2"
`)
	if _, err := readRunSpec(path); err == nil {
		t.Fatal("readRunSpec accepted an unknown field")
	}
}

// TestRunRequestFromSpecEnforcesRequiredFields proves validation happens before
// any workflow is started.
func TestRunRequestFromSpecEnforcesRequiredFields(t *testing.T) {
	cfg := specConfig()
	_, err := runRequestFromSpec(cfg, runSpec{
		RunID:      "run-1",
		Repository: "https://github.com/acme/x",
		Task:       "fix it",
		// revision missing
		AgentHarness:        "opencode2",
		VerificationProfile: "default",
	})
	if err == nil {
		t.Fatal("runRequestFromSpec accepted a manifest without a revision")
	}
}

// TestRunRequestFromSpecCarriesResourcesAndOverrides proves the manifest maps
// onto the RunRequest without inventing policy.
func TestRunRequestFromSpecCarriesResourcesAndOverrides(t *testing.T) {
	cfg := specConfig()
	review := true
	req, err := runRequestFromSpec(cfg, runSpec{
		RunID:               "run-1",
		ChangeID:            "change-1",
		ParentRunID:         "run-0",
		Scope:               "payments",
		Repository:          "https://github.com/acme/x",
		Revision:            "abc",
		Task:                "fix it",
		AgentHarness:        "opencode2",
		Model:               "fast",
		Workspace:           "python",
		EgressPolicy:        "locked",
		VerificationProfile: "default",
		Review:              &review,
		AgentTimeout:        "5m",
	})
	if err != nil {
		t.Fatalf("runRequestFromSpec: %v", err)
	}
	if req.ChangeID != "change-1" || req.ParentRunID != "run-0" {
		t.Fatalf("lineage not carried: %+v", req)
	}
	if req.Scope != "payments" || req.Model != "fast" || req.Workspace != "python" || req.EgressPolicy != "locked" {
		t.Fatalf("resources not carried: %+v", req)
	}
	if req.Review == nil || !*req.Review {
		t.Fatal("review override not carried")
	}
	if req.AgentTimeout.Minutes() != 5 {
		t.Fatalf("agent timeout = %s, want 5m", req.AgentTimeout)
	}
	// The template defaults to the deployment's, never to a manifest value the
	// operator did not approve.
	if req.SandboxTemplate != "tpl-default" {
		t.Fatalf("sandbox template = %q, want the configured default", req.SandboxTemplate)
	}
	// A declared model name is resolved as a resource AND passed to the harness.
	if req.AgentModel != "fast" {
		t.Fatalf("agent model = %q, want the raw name passed through", req.AgentModel)
	}
}

// TestRunRequestFromSpecKeepsRawModelAsHarnessOverride proves an undeclared
// model id is passed through verbatim instead of being rejected as an unknown
// resource, preserving the pre-existing --model behaviour.
func TestRunRequestFromSpecKeepsRawModelAsHarnessOverride(t *testing.T) {
	cfg := specConfig()
	req, err := runRequestFromSpec(cfg, runSpec{
		RunID: "run-1", Repository: "https://github.com/acme/x", Revision: "abc", Task: "t",
		AgentHarness: "opencode2", VerificationProfile: "default",
		Model: "claude-sonnet-4-5-20260101",
	})
	if err != nil {
		t.Fatalf("runRequestFromSpec rejected a raw model id: %v", err)
	}
	if req.Model != "" {
		t.Fatalf("model resource = %q, want empty for an undeclared id", req.Model)
	}
	if req.AgentModel != "claude-sonnet-4-5-20260101" {
		t.Fatalf("agent model = %q, want the raw id passed through", req.AgentModel)
	}
}

func TestRunRequestFromSpecRejectsBadDuration(t *testing.T) {
	cfg := specConfig()
	_, err := runRequestFromSpec(cfg, runSpec{
		RunID: "run-1", Repository: "https://github.com/acme/x", Revision: "abc", Task: "t",
		AgentHarness: "opencode2", VerificationProfile: "default", AgentTimeout: "five minutes",
	})
	if err == nil {
		t.Fatal("runRequestFromSpec accepted an unparsable duration")
	}
}

func TestTruncateAndOrDash(t *testing.T) {
	if got := truncate("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate = %q", got)
	}
	if got := truncate("ab", 4); got != "ab" {
		t.Fatalf("truncate short = %q", got)
	}
	if got := orDash("  "); got != "-" {
		t.Fatalf("orDash empty = %q", got)
	}
	if got := orDash("x"); got != "x" {
		t.Fatalf("orDash = %q", got)
	}
}
