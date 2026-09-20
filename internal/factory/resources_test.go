package factory

import (
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/tomlx"
)

// These tests cover the declarative resource layer: named models, egress
// policies, workspaces, budgets and scopes. The properties that matter are the
// ones an operator relies on for safety:
//
//   - a scope narrows, never widens;
//   - an unknown name is rejected instead of silently falling back to nothing;
//   - budgets report recorded usage but never change execution limits;
//   - digests change when content changes and do not carry credential values.

func boolPtr(v bool) *bool { return &v }

// resourceConfig builds a configuration with one of each resource kind.
func resourceConfig() Config {
	cfg := Default()
	cfg.Cube.APIURL = "http://127.0.0.1:4000"
	cfg.Cube.TemplateID = "tpl-test"
	cfg.Repositories.Allowed = []string{"https://github.com/acme/", "https://github.com/other/"}
	cfg.Models = map[string]ModelConfig{
		"fast": {Provider: "anthropic", Model: "claude-fast", APIKeyEnv: "ANTHROPIC_API_KEY"},
	}
	cfg.Egress = map[string]EgressPolicyConfig{
		"locked":   {AllowInternet: boolPtr(false), AllowOut: []string{"api.anthropic.com"}},
		"open":     {},
		"internal": {AllowOut: []string{"proxy.internal"}},
	}
	cfg.Workspaces = map[string]WorkspaceConfig{
		"python": {BasePackages: []string{"git", "python3"}, SnapshotID: "snap-1"},
	}
	cfg.Budgets = map[string]BudgetConfig{
		DefaultBudget: {},
		"cheap":       {MaxCostUSD: 1.5, MaxTokens: 1000, MaxWallClock: tomlx.FromStd(10 * 60 * 1e9)},
	}
	cfg.Scopes = map[string]ScopeConfig{
		DefaultScope: {},
		"payments": {
			Repositories: []string{"https://github.com/acme/payments"},
			Harnesses:    []string{"opencode2"},
			Egress:       "locked",
			Model:        "fast",
			Workspace:    "python",
			Budget:       "cheap",
		},
	}
	cfg.Harnesses = map[string]HarnessConfig{
		"opencode2": {Type: "opencode", Timeout: tomlx.FromStd(30 * 1e9)},
	}
	return cfg
}

// TestResolveResourcesAppliesScopeDefaults proves that naming a scope selects
// the scope's declared resources without the caller repeating them.
func TestResolveResourcesAppliesScopeDefaults(t *testing.T) {
	cfg := resourceConfig()
	req := RunRequest{
		RunID:               "run-1",
		Scope:               "payments",
		Repository:          "https://github.com/acme/payments",
		Revision:            "abc",
		Task:                "fix it",
		AgentHarness:        "opencode2",
		VerificationProfile: "default",
	}
	resolved, err := cfg.ResolveResources(req)
	if err != nil {
		t.Fatalf("ResolveResources: %v", err)
	}
	if resolved.Scope != "payments" {
		t.Fatalf("scope = %q, want payments", resolved.Scope)
	}
	if resolved.EgressPolicy != "locked" || resolved.EgressDigest == "" {
		t.Fatalf("egress = %q digest %q, want the scope's locked policy", resolved.EgressPolicy, resolved.EgressDigest)
	}
	if resolved.Model != "fast" || resolved.ModelProvider != "anthropic" {
		t.Fatalf("model = %q/%q, want fast/anthropic", resolved.Model, resolved.ModelProvider)
	}
	if resolved.Workspace != "python" || resolved.WorkspaceDigest == "" {
		t.Fatalf("workspace = %q digest %q, want python with a digest", resolved.Workspace, resolved.WorkspaceDigest)
	}
	if resolved.Budget != "cheap" {
		t.Fatalf("budget = %q, want cheap", resolved.Budget)
	}
	if resolved.Egress.AllowInternet == nil || *resolved.Egress.AllowInternet {
		t.Fatal("egress allow_internet = nil or true, want an explicit false")
	}
}

func TestResolveResourcesPinsEffectiveWorkspaceDefaults(t *testing.T) {
	cfg := resourceConfig()
	cfg.Sandbox.BasePackages = []string{"git", "ca-certificates"}
	cfg.Sandbox.SetupScript = "echo original"
	cfg.Workspaces["python"] = WorkspaceConfig{SnapshotID: "snap-1"}
	req := RunRequest{
		RunID: "run-1", Scope: "payments", Repository: "https://github.com/acme/payments",
		Revision: "abc", Task: "fix it", AgentHarness: "opencode2",
	}
	resolved, err := cfg.ResolveResources(req)
	if err != nil {
		t.Fatalf("ResolveResources: %v", err)
	}
	digest := resolved.WorkspaceDigest
	cfg.Sandbox.BasePackages = []string{"curl"}
	cfg.Sandbox.SetupScript = "echo changed"
	if got := strings.Join(resolved.WorkspaceConfig.BasePackages, ","); got != "git,ca-certificates" {
		t.Fatalf("resolved packages changed with live config: %q", got)
	}
	if resolved.WorkspaceConfig.SetupScript != "echo original" {
		t.Fatalf("resolved setup = %q, want pinned original", resolved.WorkspaceConfig.SetupScript)
	}
	if resolved.WorkspaceDigest != digest {
		t.Fatal("workspace digest changed after resolution")
	}
}

// TestScopeCannotWidenRepositoryPolicy is the security property: a scope may
// only narrow the operator's global allowlist.
func TestScopeCannotWidenRepositoryPolicy(t *testing.T) {
	cfg := resourceConfig()
	// The scope is declared with a repository the global policy allows, but a
	// request for a DIFFERENT globally-allowed repository must be refused.
	req := RunRequest{
		RunID:        "run-1",
		Scope:        "payments",
		Repository:   "https://github.com/other/secret-service",
		Revision:     "abc",
		Task:         "fix it",
		AgentHarness: "opencode2",
	}
	if _, err := cfg.ResolveResources(req); err == nil {
		t.Fatal("ResolveResources accepted a repository outside the scope's allowlist")
	}
	// A scope's allowlist can never add a repository the global policy denies,
	// because the resolution intersects the two lists.
	cfg.Scopes["payments"] = ScopeConfig{Repositories: []string{"https://github.com/attacker/"}}
	req.Repository = "https://github.com/attacker/payload"
	if _, err := cfg.ResolveResources(req); err == nil {
		t.Fatal("ResolveResources allowed a scope to widen the global allowlist")
	}
}

// TestUnknownResourceNameIsRejected proves that a typo fails loudly instead of
// silently running with no policy.
func TestUnknownResourceNameIsRejected(t *testing.T) {
	cfg := resourceConfig()
	cases := []struct {
		name string
		req  RunRequest
	}{
		{"unknown scope", RunRequest{RunID: "r", Scope: "nope", Repository: "https://github.com/acme/x", Revision: "a", Task: "t", AgentHarness: "opencode2"}},
		{"unknown workspace", RunRequest{RunID: "r", Workspace: "nope", Repository: "https://github.com/acme/x", Revision: "a", Task: "t", AgentHarness: "opencode2"}},
		{"unknown egress", RunRequest{RunID: "r", EgressPolicy: "nope", Repository: "https://github.com/acme/x", Revision: "a", Task: "t", AgentHarness: "opencode2"}},
		{"unknown model", RunRequest{RunID: "r", Model: "nope", Repository: "https://github.com/acme/x", Revision: "a", Task: "t", AgentHarness: "opencode2"}},
		{"harness outside scope", RunRequest{RunID: "r", Scope: "payments", Repository: "https://github.com/acme/payments", Revision: "a", Task: "t", AgentHarness: "other-harness"}},
	}
	for _, tc := range cases {
		if _, err := cfg.ResolveResources(tc.req); err == nil {
			t.Errorf("%s: ResolveResources accepted an unknown name", tc.name)
		}
	}
}

// TestDigestsAreStableAndContentSensitive proves digests are reproducible
// evidence rather than decoration.
func TestDigestsAreStableAndContentSensitive(t *testing.T) {
	model := ModelConfig{Provider: "anthropic", Model: "m", APIKeyEnv: "KEY"}
	if model.Digest() != model.Digest() {
		t.Fatal("model digest is not stable across calls")
	}
	changed := model
	changed.Model = "m2"
	if model.Digest() == changed.Digest() {
		t.Fatal("model digest did not change when the model changed")
	}
	// The credential NAME is part of the digest; the credential VALUE is not
	// present in the structure at all, so it cannot leak into evidence.
	renamed := model
	renamed.APIKeyEnv = "OTHER_KEY"
	if model.Digest() == renamed.Digest() {
		t.Fatal("model digest ignored the credential variable name")
	}
	if strings.Contains(model.Digest(), "KEY") {
		t.Fatal("model digest contains the credential name in plain text")
	}
}

func TestModelBaseURLRejectsCredentialsAndTokens(t *testing.T) {
	cases := []string{
		"https://user:password@gateway.example/v1",
		"https://gateway.example/v1?token=secret",
		"https://gateway.example/v1#signed-token",
	}
	for _, baseURL := range cases {
		model := ModelConfig{Provider: "test", Model: "model", BaseURL: baseURL}
		if err := model.Validate("unsafe"); err == nil {
			t.Errorf("Validate accepted credential-bearing base_url %q", baseURL)
		}
	}
	if err := (ModelConfig{Provider: "test", Model: "model", BaseURL: "https://gateway.example/v1"}).Validate("safe"); err != nil {
		t.Fatalf("Validate rejected safe base_url: %v", err)
	}
}

// TestBudgetExceededReportsRecordedUsageOnly proves a budget is evidence: it
// reports what was recorded and never invents a number.
func TestBudgetExceededReportsRecordedUsageOnly(t *testing.T) {
	budget := BudgetConfig{MaxCostUSD: 1.0, MaxTokens: 100}
	if over := budget.Exceeded(0.5, 50, 0); len(over) != 0 {
		t.Fatalf("Exceeded reported %v for usage within budget", over)
	}
	over := budget.Exceeded(1.5, 500, 0)
	if len(over) != 2 {
		t.Fatalf("Exceeded reported %v, want both ceilings", over)
	}
	if over := (BudgetConfig{}).Exceeded(1e9, 1e9, time.Hour); len(over) != 0 {
		t.Fatalf("a zero budget must disable enforcement, got %v", over)
	}
}

func TestBudgetWallClockIsEvidenceOnly(t *testing.T) {
	budget := BudgetConfig{MaxWallClock: tomlx.FromStd(time.Minute)}
	if over := budget.Exceeded(0, 0, 30*time.Second); len(over) != 0 {
		t.Fatalf("within-budget duration reported as exceeded: %v", over)
	}
	over := budget.Exceeded(0, 0, 2*time.Minute)
	if len(over) != 1 || !strings.Contains(over[0], "wall clock") {
		t.Fatalf("wall-clock evidence = %v, want one crossing", over)
	}
}

// TestResolvedDigestCoversEveryResource proves the run's resource digest
// changes when any resolved resource changes, so evidence cannot claim two
// different policies were the same run configuration.
func TestResolvedDigestCoversEveryResource(t *testing.T) {
	cfg := resourceConfig()
	req := RunRequest{
		RunID:        "run-1",
		Scope:        "payments",
		Repository:   "https://github.com/acme/payments",
		Revision:     "abc",
		Task:         "fix it",
		AgentHarness: "opencode2",
	}
	first, err := cfg.ResolveResources(req)
	if err != nil {
		t.Fatalf("ResolveResources: %v", err)
	}
	cfg.Models["fast"] = ModelConfig{Provider: "anthropic", Model: "different", APIKeyEnv: "ANTHROPIC_API_KEY"}
	second, err := cfg.ResolveResources(req)
	if err != nil {
		t.Fatalf("ResolveResources: %v", err)
	}
	if first.Digest() == second.Digest() {
		t.Fatal("resource digest did not change when a resolved model changed")
	}
}

// TestRawAgentModelIsNotTreatedAsAResourceName preserves the pre-existing
// --model behaviour: a raw model id is passed to the harness verbatim, while
// only a declared model name is resolved and pinned as a resource.
func TestRawAgentModelIsNotTreatedAsAResourceName(t *testing.T) {
	cfg := resourceConfig()
	req := RunRequest{
		RunID:        "run-1",
		Repository:   "https://github.com/acme/x",
		Revision:     "abc",
		Task:         "t",
		AgentHarness: "opencode2",
		AgentModel:   "claude-sonnet-4-5-20260101",
	}
	resolved, err := cfg.ResolveResources(req)
	if err != nil {
		t.Fatalf("ResolveResources rejected a raw model id: %v", err)
	}
	if resolved.Model != "" {
		t.Fatalf("resolved model = %q, want empty for a raw id", resolved.Model)
	}
	// A declared name still resolves.
	req.Model = "fast"
	resolved, err = cfg.ResolveResources(req)
	if err != nil {
		t.Fatalf("ResolveResources: %v", err)
	}
	if resolved.Model != "fast" || resolved.ModelID != "claude-fast" {
		t.Fatalf("resolved model = %q/%q, want fast/claude-fast", resolved.Model, resolved.ModelID)
	}
}
