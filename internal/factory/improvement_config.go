package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/harnessprofile"
	"github.com/mitkox/esf/internal/inferencebudget"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

type ImprovementConfig struct {
	UpstreamURL        string            `toml:"upstream_url"`
	WireModel          string            `toml:"wire_model"`
	ApprovalKeys       map[string]string `toml:"approval_keys"`
	GatewayControlURL  string            `toml:"gateway_control_url"`
	GatewayPublicURL   string            `toml:"gateway_public_url"`
	GatewayAdminKeyEnv string            `toml:"gateway_admin_key_env"`
	GatewayTokenKeyEnv string            `toml:"gateway_token_key_env"`
	RouteDigest        string            `toml:"route_digest"`
	PricingEvidence    string            `toml:"pricing_evidence"`
	InputPrice         int64             `toml:"input_micros_per_million"`
	OutputPrice        int64             `toml:"output_micros_per_million"`
}

type ImprovementPolicy struct {
	Mode                 string         `toml:"mode" json:"mode"`
	Enabled              bool           `toml:"enabled" json:"enabled"`
	Profile              string         `toml:"profile" json:"profile"`
	EditorHarness        string         `toml:"editor_harness" json:"editor_harness"`
	Model                string         `toml:"model" json:"model"`
	MaxCostMicros        int64          `toml:"max_cost_micros" json:"max_cost_micros"`
	BaselineCostMicros   int64          `toml:"baseline_cost_micros" json:"baseline_cost_micros"`
	BaselineDuration     tomlx.Duration `toml:"baseline_duration" json:"baseline_duration"`
	MaxDuration          tomlx.Duration `toml:"max_duration" json:"max_duration"`
	CleanupAllowance     tomlx.Duration `toml:"cleanup_allowance" json:"cleanup_allowance"`
	MaxInputTokens       int64          `toml:"max_input_tokens" json:"max_input_tokens"`
	MaxOutputTokens      int64          `toml:"max_output_tokens" json:"max_output_tokens"`
	MaxRequests          int            `toml:"max_requests" json:"max_requests"`
	EnrollmentPercent    int            `toml:"enrollment_percent" json:"enrollment_percent"`
	QualificationProfile string         `toml:"qualification_profile" json:"qualification_profile"`
	RolloutReceipt       string         `toml:"rollout_receipt" json:"rollout_receipt"`
}

func (p ImprovementPolicy) Digest() string {
	// Activation/approval references wrap the frozen experiment specification;
	// including the qualification digest would make the receipt self-referential.
	p.Enabled = false
	p.Mode = ""
	p.QualificationProfile = ""
	p.EnrollmentPercent = 0
	p.RolloutReceipt = ""
	return harnessprofile.Hash(p)
}
func (p ImprovementPolicy) Validate() error {
	if p.Mode != "offline" && p.Mode != "shadow" && p.Mode != "adaptive" {
		return errors.New("improvement mode must be offline, shadow, or adaptive")
	}
	if (p.Profile != "" && !harnessprofile.Name.MatchString(p.Profile)) || !harnessprofile.Name.MatchString(p.EditorHarness) || !harnessprofile.Name.MatchString(p.Model) || p.MaxCostMicros <= 0 || p.MaxDuration.Std() <= 0 || p.CleanupAllowance.Std() < time.Minute || p.MaxDuration.Std() <= p.CleanupAllowance.Std() || p.MaxRequests < 8 || p.MaxRequests > 256 || p.MaxInputTokens <= 4096 || p.MaxInputTokens > 1e6 || p.MaxOutputTokens <= 0 || p.MaxOutputTokens > 1e6 {
		return errors.New("improvement requires bounded model sessions, profile, model, cost, duration and cleanup allowance")
	}
	if p.Mode != "offline" && (p.BaselineCostMicros <= 0 || p.BaselineDuration.Std() <= 0 || p.MaxCostMicros > 5*p.BaselineCostMicros || p.MaxDuration.Std() > 5*p.BaselineDuration.Std() || p.EnrollmentPercent < 1 || p.EnrollmentPercent > 5) {
		return errors.New("live policy exceeds the one-round 5x pilot envelope")
	}
	if p.Mode != "offline" && p.Profile == "" {
		return errors.New("live policies require a pinned qualified starting profile")
	}
	return nil
}
func (c Config) ProfileStore() (*harnessprofile.Store, error) {
	return harnessprofile.Open(filepath.Join(c.Storage.DataDir, "harness-profiles"))
}
func (c Config) validateImprovement() error {
	for name, d := range c.HarnessProfiles {
		if !harnessprofile.Name.MatchString(name) || !harnessprofile.DigestPattern.MatchString(d) {
			return fmt.Errorf("invalid harness profile %q", name)
		}
	}
	for name, scope := range c.Scopes {
		for _, profile := range scope.HarnessProfiles {
			if c.HarnessProfiles[profile] == "" {
				return fmt.Errorf("scope %s references an unknown instructional profile", name)
			}
		}
		for _, policy := range scope.ImprovementPolicies {
			if _, ok := c.ImprovementPolicies[policy]; !ok {
				return fmt.Errorf("scope %s references an unknown improvement policy", name)
			}
		}
	}
	for name, p := range c.ImprovementPolicies {
		if !harnessprofile.Name.MatchString(name) {
			return errors.New("invalid improvement policy name")
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("policy %s: %w", name, err)
		}
		if p.Profile != "" && c.HarnessProfiles[p.Profile] == "" {
			return fmt.Errorf("policy %s: unknown harness profile", name)
		}
		h, ok := c.Harnesses[p.EditorHarness]
		if !ok || h.Type != "opencode" || !h.Preinstalled || h.BinarySHA256 == "" || len(h.Packages) > 0 || h.CredentialMode != cubeEgressCredentialMode || len(h.PassEnv) > 0 || len(h.ProviderFiles) > 0 {
			return errors.New("improvement editor must be an egress-managed OpenCode harness")
		}
		m, ok := c.Models[p.Model]
		if !ok || m.BaseURL != c.Improvement.GatewayPublicURL {
			return errors.New("improvement model and editor must use the budget gateway")
		}
	}
	for name, path := range c.ImprovementSuites {
		if !harnessprofile.Name.MatchString(name) || !filepath.IsAbs(path) {
			return errors.New("suite names and absolute operator paths required")
		}
	}
	if len(c.ImprovementPolicies) > 0 {
		g := c.Improvement
		if g.RouteDigest != inferencebudget.RouteDigest(g.UpstreamURL, g.WireModel, g.InputPrice, g.OutputPrice, g.PricingEvidence) {
			return errors.New("improvement route digest does not match upstream, wire model and pricing")
		}
		u, e := url.Parse(g.GatewayPublicURL)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimRight(u.Path, "/") != "/v1" {
			return errors.New("gateway public URL must be an HTTPS /v1 endpoint")
		}
		if e := validateCredentialControlPlane(g.GatewayControlURL); e != nil {
			return e
		}
		if g.GatewayAdminKeyEnv == "" || g.GatewayTokenKeyEnv == "" || g.GatewayAdminKeyEnv == g.GatewayTokenKeyEnv || !harnessprofile.DigestPattern.MatchString(g.RouteDigest) || !harnessprofile.DigestPattern.MatchString(g.PricingEvidence) || g.InputPrice <= 0 || g.OutputPrice <= 0 || len(c.Sandbox.RuntimeAllowOut) > 0 {
			return errors.New("improvement requires qualified pricing, separate gateway credentials and no extra runtime egress")
		}
	}
	return nil
}

func (a *Activities) resolveInstructionProfile(req RunRequest, res *ResolvedResources) error {
	if req.HarnessProfile == "" {
		return nil
	}
	scope := a.cfg.Scopes[res.Scope]
	// Unlike legacy resources, newly introduced profiles are default closed.
	if !containsString(scope.HarnessProfiles, req.HarnessProfile) {
		return errors.New("harness profile is not granted to this scope")
	}
	d, ok := a.cfg.HarnessProfiles[req.HarnessProfile]
	if !ok {
		return errors.New("unknown harness profile")
	}
	s, err := a.cfg.ProfileStore()
	if err != nil {
		return err
	}
	defer s.Close()
	p, _, err := s.Authorized(d, a.cfg.Improvement.ApprovalKeys, time.Now(), func(in harnessprofile.QualityInput) bool {
		return in.RouteDigest == a.cfg.Improvement.RouteDigest
	})
	if err != nil {
		return fmt.Errorf("profile is not approved: %w", err)
	}
	model := res.ModelID
	if model == "" {
		model = req.AgentModel
	}
	if model == "" {
		model = a.cfg.Harnesses[req.AgentHarness].Model
	}
	repo := req.Repository
	if repo == "" {
		repo = "local:" + req.LocalPath
	}
	if p.Binding != (harnessprofile.Binding{Scope: res.Scope, Repository: repo, Harness: req.AgentHarness, Model: model, HarnessDigest: "sha256:" + strings.ToLower(a.cfg.Harnesses[req.AgentHarness].BinarySHA256), Template: res.WorkspaceConfig.Template}) {
		return errors.New("profile scope, repository, harness or model mismatch")
	}
	hc := a.cfg.Harnesses[req.AgentHarness]
	if hc.Type != "opencode" || !hc.Preinstalled || hc.BinarySHA256 == "" || hc.CredentialMode != cubeEgressCredentialMode {
		return errors.New("instruction profiles are only qualified for OpenCode2")
	}
	g := a.cfg.Improvement
	endpoint := res.ModelBaseURL
	if endpoint == "" {
		endpoint = hc.BaseURL
	}
	wire := model
	if _, m, ok := strings.Cut(wire, "/"); ok {
		wire = m
	}
	wire, _, _ = strings.Cut(wire, "#")
	if (endpoint != g.UpstreamURL && endpoint != g.GatewayPublicURL) || wire != g.WireModel || g.RouteDigest != inferencebudget.RouteDigest(g.UpstreamURL, g.WireModel, g.InputPrice, g.OutputPrice, g.PricingEvidence) {
		return errors.New("profile model route or pricing changed since qualification")
	}
	res.HarnessProfile = req.HarnessProfile
	res.HarnessProfileDigest = d
	return nil
}
func (a *Activities) applyInstructionProfile(ctx context.Context, sb sandbox.Sandbox, d, prompt string) (string, error) {
	s, e := a.cfg.ProfileStore()
	if e != nil {
		return "", e
	}
	defer s.Close()
	p, e := s.Get(d)
	if e != nil {
		return "", e
	}
	root := "/workspace/.factory/harness-profiles/" + d[7:]
	keys := make([]string, 0, len(p.Components))
	for k := range p.Components {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if e := sb.WriteFile(ctx, root+"/"+k, []byte(p.Components[k])); e != nil {
			return "", e
		}
	}
	return p.Prompt(root, prompt), nil
}
func (c Config) gatewayClient() (inferencebudget.Client, error) {
	k := os.Getenv(c.Improvement.GatewayAdminKeyEnv)
	if len(k) < 32 {
		return inferencebudget.Client{}, errors.New("gateway control credential unavailable")
	}
	return inferencebudget.Client{URL: c.Improvement.GatewayControlURL, Key: k}, nil
}
func (c Config) improvementNetwork(name, account string) (sandbox.Network, bool, error) {
	if !inferencebudget.ID.MatchString(account) {
		return sandbox.Network{}, false, errors.New("invalid budget account")
	}
	h := c.Harnesses[name]
	if h.Type != "opencode" || h.CredentialMode != cubeEgressCredentialMode {
		return sandbox.Network{}, false, errors.New("harness is not bound to the budget gateway")
	}
	h.BaseURL = c.Improvement.GatewayPublicURL
	k := os.Getenv(c.Improvement.GatewayTokenKeyEnv)
	if len(k) < 32 {
		return sandbox.Network{}, false, errors.New("gateway token credential unavailable")
	}
	n, err := c.networkForHarnessCredential(name, h, inferencebudget.Token([]byte(k), account))
	if err != nil {
		return n, false, err
	}
	for i := range n.Rules {
		n.Rules[i].Match.Path = "/v1/chat/completions"
	}
	return n, true, nil
}

// ImprovementReceipt is additive evidence. It never changes the ordinary
// agent, verification, review or cleanup verdicts in a run manifest.
type ImprovementReceipt struct {
	StartedAt           time.Time               `json:"started_at"`
	SelectedPatchDigest string                  `json:"selected_patch_digest,omitempty"`
	Schema              string                  `json:"schema"`
	Policy              string                  `json:"policy"`
	PolicyDigest        string                  `json:"policy_digest"`
	Mode                string                  `json:"mode"`
	Upstream            string                  `json:"upstream"`
	LibraryDigest       string                  `json:"library_digest"`
	SelectedRun         string                  `json:"selected_run,omitempty"`
	ProfileDigest       string                  `json:"profile_digest,omitempty"`
	Stages              []ImprovementStage      `json:"stages"`
	Budget              inferencebudget.Account `json:"budget"`
	Fallback            string                  `json:"fallback,omitempty"`
	Error               string                  `json:"error,omitempty"`
	FailureKind         string                  `json:"failure_kind,omitempty"`
}
type ImprovementStage struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Profile string `json:"profile,omitempty"`
	Run     string `json:"run,omitempty"`
	Error   string `json:"error,omitempty"`
}

func receiptBytes(v any) []byte { b, _ := json.Marshal(v); return b }
