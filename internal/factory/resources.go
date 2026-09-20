package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

// This file defines the factory's declarative resources.
//
// The design borrows one idea from Google's AX: the things an agent needs to
// start warm — a workspace, an egress policy, a model endpoint — are RESOURCES
// declared by the operator, not fields improvised per run. A run REFERENCES
// them by name. That gives three properties the MVP lacked:
//
//  1. Every resolved resource has a digest, so a run's evidence says exactly
//     which workspace/egress/model was in effect, and a change to any of them
//     is a reviewable diff rather than an invisible drift.
//  2. A resource can be validated once at startup instead of trusted per run.
//  3. Tenancy becomes expressible: a scope grants a subset of resources instead
//     of every caller seeing everything.
//
// Resources remain OPERATOR configuration. A caller can name one only if the
// scope it is running under grants it; it can never define one.

// ModelConfig declares one model endpoint the factory may use.
//
// It is separate from a harness because a harness is HOW an agent runs and a
// model is WHAT it thinks with: keeping them apart lets an operator change the
// model for every harness at once, and makes per-model cost and token
// accounting meaningful across harnesses.
type ModelConfig struct {
	// Provider is the vendor identifier recorded in evidence, for example
	// "anthropic" or "openai".
	Provider string `toml:"provider"`
	// Model is the provider's model identifier.
	Model string `toml:"model"`
	// APIKeyEnv names the HOST environment variable holding the credential.
	// The value never appears in configuration, evidence, or a sandbox unless
	// the harness is explicitly granted it.
	APIKeyEnv string `toml:"api_key_env"`
	// BaseURL optionally points the harness at a gateway.
	BaseURL string `toml:"base_url"`
	// Scopes restricts which scopes may use this model. Empty means every
	// scope.
	Scopes []string `toml:"scopes"`
}

// Validate reports whether the model declaration is usable.
func (m ModelConfig) Validate(name string) error {
	var problems []string
	if strings.TrimSpace(m.Provider) == "" {
		problems = append(problems, "provider is required")
	}
	if strings.TrimSpace(m.Model) == "" {
		problems = append(problems, "model is required")
	}
	if strings.TrimSpace(m.APIKeyEnv) == "" && strings.TrimSpace(m.BaseURL) == "" {
		problems = append(problems, "either api_key_env or base_url is required")
	}
	if raw := strings.TrimSpace(m.BaseURL); raw != "" {
		parsed, err := url.Parse(raw)
		switch {
		case err != nil || parsed.Scheme == "" || parsed.Host == "":
			problems = append(problems, "base_url must be an absolute URL")
		case parsed.Scheme != "http" && parsed.Scheme != "https":
			problems = append(problems, "base_url must use http or https")
		case parsed.User != nil:
			problems = append(problems, "base_url must not embed credentials")
		case parsed.RawQuery != "" || parsed.Fragment != "":
			// A model endpoint is a stable base URL. Query strings and fragments
			// are both unnecessary here and are common places for signed tokens to
			// leak into the inventory and durable evidence.
			problems = append(problems, "base_url must not contain a query or fragment")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("model %q: %s", name, strings.Join(problems, "; "))
	}
	return nil
}

// Digest is a stable content hash of the model declaration.
//
// The credential NAME is part of the digest; the credential VALUE never is,
// because it is not present in this structure.
func (m ModelConfig) Digest() string { return digestOf(m) }

// EgressPolicyConfig is a named network policy for a sandbox.
//
// Deny-by-default is the operator's choice, not the factory's default: leaving
// AllowInternet nil and AllowOut empty means "deployment default", which is
// exactly what the MVP did. A policy that sets AllowInternet=false is the
// strong form, and the factory records which one was in effect.
type EgressPolicyConfig struct {
	// AllowInternet, when non-nil, enables or disables public egress.
	AllowInternet *bool `toml:"allow_internet"`
	// AllowOut lists explicitly permitted destinations.
	AllowOut []string `toml:"allow_out"`
	// DenyOut lists explicitly denied destinations. Deny wins.
	DenyOut []string `toml:"deny_out"`
}

// Digest is a stable content hash of the policy.
func (e EgressPolicyConfig) Digest() string { return digestOf(e) }

// Network converts the policy to the sandbox abstraction's request type.
func (e EgressPolicyConfig) Network() sandbox.Network {
	return sandbox.Network{
		AllowOut:      append([]string(nil), e.AllowOut...),
		DenyOut:       append([]string(nil), e.DenyOut...),
		AllowInternet: e.AllowInternet,
	}
}

// Summary is a human-readable one-line description used by `factory describe`.
func (e EgressPolicyConfig) Summary() string {
	internet := "deployment-default"
	if e.AllowInternet != nil {
		if *e.AllowInternet {
			internet = "allowed"
		} else {
			internet = "denied"
		}
	}
	var parts []string
	parts = append(parts, "internet="+internet)
	if len(e.AllowOut) > 0 {
		parts = append(parts, "allow_out="+strings.Join(e.AllowOut, ","))
	}
	if len(e.DenyOut) > 0 {
		parts = append(parts, "deny_out="+strings.Join(e.DenyOut, ","))
	}
	return strings.Join(parts, " ")
}

// WorkspaceConfig declares a pre-warmed execution environment.
//
// It is the AX Workspace idea reduced to what the factory can honestly support
// today: the factory-owned setup (packages, setup script) plus an optional
// snapshot that already contains it. The digest is recorded on every run, so
// "same workspace, different result" is investigable.
type WorkspaceConfig struct {
	// Template overrides the deployment's default sandbox template.
	Template string `toml:"template"`
	// BasePackages are installed before the harness is provisioned. When empty,
	// the factory-wide sandbox base packages apply.
	BasePackages []string `toml:"base_packages"`
	// SetupScript is factory-owned preparation that a package manager cannot
	// express. It must never contain task-derived text.
	SetupScript string `toml:"setup_script"`
	// SnapshotID, when set, names a pre-built CubeSandbox snapshot that already
	// contains this workspace's preparation. Recorded as evidence; wiring the
	// clone path is the Phase 2 warm start.
	SnapshotID string `toml:"snapshot_id"`
	// RepositoryCache warms the repository clone before the agent starts. It is
	// declared per workspace because it changes what the sandbox contains.
	RepositoryCache bool `toml:"repository_cache"`
}

// Validate reports whether the workspace declaration is usable.
func (w WorkspaceConfig) Validate(name string) error {
	if strings.ContainsAny(w.SetupScript, "\x00") {
		return fmt.Errorf("workspace %q: setup_script contains a NUL byte", name)
	}
	return nil
}

// Digest is a stable content hash of the workspace declaration.
func (w WorkspaceConfig) Digest() string { return digestOf(w) }

// EffectiveBasePackages returns the workspace packages, falling back to the
// factory-wide list.
func (w WorkspaceConfig) EffectiveBasePackages(fallback []string) []string {
	if len(w.BasePackages) > 0 {
		return append([]string(nil), w.BasePackages...)
	}
	return append([]string(nil), fallback...)
}

// BudgetConfig is a ceiling on what a scope or run may spend.
//
// The factory cannot observe a running model gateway's meter in the MVP, so a
// budget is post-run evidence only. Cost, token and wall-clock crossings are
// reported after execution; they never rewrite the deterministic gate result
// or shorten the run's configured timeout.
type BudgetConfig struct {
	// MaxCostUSD is the ceiling on recorded inference cost. Zero disables it.
	MaxCostUSD float64 `toml:"max_cost_usd"`
	// MaxTokens is the ceiling on recorded tokens. Zero disables it.
	MaxTokens int64 `toml:"max_tokens"`
	// MaxWallClock is the reporting ceiling on a run's wall-clock duration. Zero
	// disables it; execution remains bounded by the factory-wide total timeout.
	MaxWallClock tomlx.Duration `toml:"max_wall_clock"`
}

// Validate reports whether the budget is well-formed.
func (b BudgetConfig) Validate(name string) error {
	if b.MaxCostUSD < 0 {
		return fmt.Errorf("budget %q: max_cost_usd must not be negative", name)
	}
	if b.MaxTokens < 0 {
		return fmt.Errorf("budget %q: max_tokens must not be negative", name)
	}
	if b.MaxWallClock < 0 {
		return fmt.Errorf("budget %q: max_wall_clock must not be negative", name)
	}
	return nil
}

// Digest is a stable content hash of the budget.
func (b BudgetConfig) Digest() string { return digestOf(b) }

// Exceeded reports which ceilings a finished run crossed. It is evidence, not
// enforcement: the factory never rewrites a result because a budget was hit.
func (b BudgetConfig) Exceeded(costUSD float64, tokens int64, elapsed time.Duration) []string {
	var over []string
	if b.MaxCostUSD > 0 && costUSD > b.MaxCostUSD {
		over = append(over, fmt.Sprintf("cost %.4f > %.4f USD", costUSD, b.MaxCostUSD))
	}
	if b.MaxTokens > 0 && tokens > b.MaxTokens {
		over = append(over, fmt.Sprintf("tokens %d > %d", tokens, b.MaxTokens))
	}
	if max := b.MaxWallClock.Std(); max > 0 && elapsed > max {
		over = append(over, fmt.Sprintf("wall clock %s > %s", elapsed, max))
	}
	return over
}

// ReviewConfig controls the human review gate.
//
// The gate exists to make the factory's most expensive moment — a live sandbox
// holding a built repository — survive a human decision instead of being
// destroyed and rebuilt. It is off by default: a gate that nobody configured
// would silently park runs forever.
type ReviewConfig struct {
	// Enabled turns the gate on for every run that does not override it.
	Enabled bool `toml:"enabled"`
	// Timeout bounds how long a run may stay paused. On expiry the factory
	// resumes, completes verification and cleans up exactly as normal.
	Timeout tomlx.Duration `toml:"timeout"`
	// PreviewPorts are the in-sandbox ports exposed through the deployment
	// ingress when the run pauses. Empty exposes nothing.
	PreviewPorts []int `toml:"preview_ports"`
	// Suspend checkpoints the sandbox while paused. It is honoured only when
	// the provider advertises the suspend capability; otherwise the run pauses
	// with the sandbox live and the manifest says so.
	Suspend bool `toml:"suspend"`
}

// ScopeConfig is a tenancy boundary.
//
// The AX analogue is an atespace: every resource lives inside one, and a caller
// names the scope it acts in. A scope can only narrow the operator's policy,
// never widen it — the union of all scopes is a subset of the global config.
type ScopeConfig struct {
	// Repositories narrows the global allowlist. Empty inherits the global list.
	Repositories []string `toml:"repositories"`
	// Harnesses restricts which harnesses may be used. Empty allows all.
	Harnesses []string `toml:"harnesses"`
	// Egress names an [egress.<name>] policy.
	Egress string `toml:"egress"`
	// Model names a [models.<name>] entry used when a run does not choose one.
	Model string `toml:"model"`
	// Workspace names a [workspaces.<name>] entry.
	Workspace string `toml:"workspace"`
	// Budget names a [budgets.<name>] entry.
	Budget string `toml:"budget"`
	// Review overrides the factory-wide review gate for this scope. Nil
	// inherits the global setting.
	Review *bool `toml:"review"`
}

// ResolvedResources is the fully-resolved resource set for one run.
//
// It is computed once, by ValidateRequest, and carried through the workflow as
// plain data: workflow code must never resolve a name, because a config change
// mid-run would make replay non-deterministic.
type ResolvedResources struct {
	// Scope the run executes under. Empty means the default scope.
	Scope string `json:"scope,omitempty"`
	// RepositoriesAllowed is the effective repository allowlist after the scope
	// narrowed the global one. It is recorded so a rejected repository can be
	// explained from evidence rather than from the live config.
	RepositoriesAllowed []string `json:"repositories_allowed,omitempty"`
	// Workspace name and digest.
	Workspace       string `json:"workspace,omitempty"`
	WorkspaceDigest string `json:"workspace_digest,omitempty"`
	// WorkspaceConfig is the resolved workspace declaration, included so the run
	// is reproducible from its own evidence.
	WorkspaceConfig WorkspaceConfig `json:"workspace_config,omitempty"`
	// Egress policy name and digest. The effective network request is included
	// because digests are only verifiable against the policy content.
	EgressPolicy  string          `json:"egress_policy,omitempty"`
	EgressDigest  string          `json:"egress_digest,omitempty"`
	Egress        sandbox.Network `json:"egress"`
	EgressSummary string          `json:"egress_summary,omitempty"`
	// Model name, provider, model id and digest. Credential names are recorded;
	// credential values are not.
	Model          string `json:"model,omitempty"`
	ModelProvider  string `json:"model_provider,omitempty"`
	ModelID        string `json:"model_id,omitempty"`
	ModelDigest    string `json:"model_digest,omitempty"`
	ModelAPIKeyEnv string `json:"model_api_key_env,omitempty"`
	ModelBaseURL   string `json:"model_base_url,omitempty"`
	// Budget name and digest.
	Budget       string `json:"budget,omitempty"`
	BudgetDigest string `json:"budget_digest,omitempty"`
	// BudgetConfig is the resolved budget, included for the same reason as
	// WorkspaceConfig.
	BudgetConfig BudgetConfig `json:"budget_config,omitempty"`
	// Review is the effective review-gate setting.
	Review bool `json:"review"`
	// ReviewTimeout bounds a paused run.
	ReviewTimeout DurationJSON `json:"review_timeout,omitempty"`
	// PreviewPorts exposed when a run pauses.
	PreviewPorts []int `json:"preview_ports,omitempty"`
	// Suspend requests checkpointing while paused.
	Suspend bool `json:"suspend,omitempty"`
}

// Digest is a stable hash of the whole resolved resource set, recorded in the
// manifest so a run's policy is reproducible from evidence alone.
func (r ResolvedResources) Digest() string { return digestOf(r) }

// digestOf canonicalises a value to JSON and returns its SHA-256.
//
// JSON is used rather than a hand-rolled string because encoding/json sorts
// map keys, so the digest is stable across processes and machines.
func digestOf(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		// A resource that cannot be marshalled cannot be resolved; returning a
		// marker keeps the failure visible in evidence instead of empty.
		return "unhashable"
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ResolveResources resolves the resource set for a request under a scope.
//
// The scope may only narrow: a repository allowed globally but not by the scope
// is rejected, and a resource the scope does not name falls back to the
// operator's default for that resource kind, never to "no policy".
func (c Config) ResolveResources(req RunRequest) (ResolvedResources, error) {
	scopeName := req.Scope
	if scopeName == "" {
		scopeName = DefaultScope
	}
	scope, hasScope := c.Scopes[scopeName]
	if !hasScope && scopeName != DefaultScope {
		return ResolvedResources{}, fmt.Errorf("unknown scope %q", scopeName)
	}

	out := ResolvedResources{Scope: scopeName}

	// Repository narrowing: the scope allowlist is intersected with the global
	// allowlist. A scope can never add a repository the operator did not allow.
	if hasScope && len(scope.Repositories) > 0 {
		allowed := narrowedPrefixes(c.Repositories.Allowed, scope.Repositories)
		if req.Repository != "" && !matchesAnyPrefix(req.Repository, allowed) {
			return ResolvedResources{}, fmt.Errorf("repository %q is not allowed in scope %q", req.Repository, scopeName)
		}
		out.RepositoriesAllowed = allowed
	} else {
		out.RepositoriesAllowed = append([]string(nil), c.Repositories.Allowed...)
	}

	// Harness narrowing.
	if hasScope && len(scope.Harnesses) > 0 && !containsString(scope.Harnesses, req.AgentHarness) {
		return ResolvedResources{}, fmt.Errorf("harness %q is not allowed in scope %q", req.AgentHarness, scopeName)
	}

	// Workspace. Resolve the EFFECTIVE preparation here, including factory
	// defaults, so later activities never re-read mutable worker configuration.
	// The effective values travel with the workflow and, for a named workspace,
	// are covered by its digest.
	effectiveWorkspace := WorkspaceConfig{
		Template:     req.SandboxTemplate,
		BasePackages: append([]string(nil), c.Sandbox.BasePackages...),
		SetupScript:  c.Sandbox.SetupScript,
	}
	if effectiveWorkspace.Template == "" {
		effectiveWorkspace.Template = c.Cube.TemplateID
	}
	workspaceName := req.Workspace
	if workspaceName == "" && hasScope {
		workspaceName = scope.Workspace
	}
	if workspaceName != "" {
		ws, ok := c.Workspaces[workspaceName]
		if !ok {
			return ResolvedResources{}, fmt.Errorf("unknown workspace %q", workspaceName)
		}
		if ws.Template != "" {
			effectiveWorkspace.Template = ws.Template
		}
		if len(ws.BasePackages) > 0 {
			effectiveWorkspace.BasePackages = append([]string(nil), ws.BasePackages...)
		}
		if strings.TrimSpace(ws.SetupScript) != "" {
			effectiveWorkspace.SetupScript = ws.SetupScript
		}
		effectiveWorkspace.SnapshotID = ws.SnapshotID
		effectiveWorkspace.RepositoryCache = ws.RepositoryCache
		out.Workspace = workspaceName
		out.WorkspaceDigest = effectiveWorkspace.Digest()
	}
	out.WorkspaceConfig = effectiveWorkspace

	// Egress policy.
	egressName := req.EgressPolicy
	if egressName == "" && hasScope {
		egressName = scope.Egress
	}
	if egressName != "" {
		policy, ok := c.Egress[egressName]
		if !ok {
			return ResolvedResources{}, fmt.Errorf("unknown egress policy %q", egressName)
		}
		out.EgressPolicy = egressName
		out.EgressDigest = policy.Digest()
		out.Egress = policy.Network()
		out.EgressSummary = policy.Summary()
	}

	// Model. Only req.Model names a declared resource: req.AgentModel is a raw
	// harness override that has always been passed through verbatim, and
	// treating it as a resource name would break that existing behaviour.
	modelName := req.Model
	if modelName == "" && hasScope {
		modelName = scope.Model
	}
	if modelName != "" {
		model, ok := c.Models[modelName]
		if !ok {
			return ResolvedResources{}, fmt.Errorf("unknown model %q", modelName)
		}
		if len(model.Scopes) > 0 && !containsString(model.Scopes, scopeName) {
			return ResolvedResources{}, fmt.Errorf("model %q is not available in scope %q", modelName, scopeName)
		}
		out.Model = modelName
		out.ModelProvider = model.Provider
		out.ModelID = model.Model
		out.ModelDigest = model.Digest()
		out.ModelAPIKeyEnv = model.APIKeyEnv
		out.ModelBaseURL = model.BaseURL
	}

	// Budget.
	budgetName := ""
	if hasScope {
		budgetName = scope.Budget
	}
	if budgetName == "" {
		budgetName = DefaultBudget
	}
	if budget, ok := c.Budgets[budgetName]; ok {
		out.Budget = budgetName
		out.BudgetDigest = budget.Digest()
		out.BudgetConfig = budget
	}

	// Review gate: request override, then scope, then global.
	review := c.Review.Enabled
	if hasScope && scope.Review != nil {
		review = *scope.Review
	}
	if req.Review != nil {
		review = *req.Review
	}
	out.Review = review
	if review {
		timeout := c.Review.Timeout.Std()
		if timeout <= 0 {
			timeout = DefaultReviewTimeout
		}
		out.ReviewTimeout = DurationJSON(timeout)
		out.PreviewPorts = append([]int(nil), c.Review.PreviewPorts...)
		out.Suspend = c.Review.Suspend
	}

	return out, nil
}

// DefaultScope is the scope used when a caller names none.
const DefaultScope = "default"

// DefaultBudget is the budget entry applied when none is named.
const DefaultBudget = "default"

// DefaultReviewTimeout bounds a paused run when the operator did not say.
const DefaultReviewTimeout = 24 * time.Hour

// DurationJSON is a time.Duration that marshals to a readable string.
//
// The resolved resources travel through Temporal payloads and manifests, and a
// raw nanosecond count in evidence is unreadable.
type DurationJSON time.Duration

// MarshalJSON renders the duration as a Go duration string.
func (d DurationJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON accepts a duration string or a nanosecond number.
func (d *DurationJSON) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = DurationJSON(parsed)
		return nil
	}
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*d = DurationJSON(n)
	return nil
}

// Std returns the duration as a time.Duration.
func (d DurationJSON) Std() time.Duration { return time.Duration(d) }

// narrowedPrefixes returns the scope entries that the global allowlist still
// covers.
//
// Coverage, not equality, is the right relation: a scope may declare a prefix
// NARROWER than a global one (https://github.com/acme/payments inside
// https://github.com/acme/), and that is a legitimate restriction. A scope
// entry that no global prefix covers is dropped, which is what makes a scope
// unable to widen policy. Order follows the scope so a narrowing is stable and
// readable.
func narrowedPrefixes(global, scope []string) []string {
	var out []string
	for _, s := range scope {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		for _, g := range global {
			g = strings.TrimSpace(g)
			if g != "" && strings.HasPrefix(trimmed, g) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// matchesAnyPrefix reports whether value starts with any allowlisted prefix.
func matchesAnyPrefix(value string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(value, p) {
			return true
		}
	}
	return false
}

// containsString reports exact membership.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// sortedKeys returns map keys in a stable order for human-readable output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
