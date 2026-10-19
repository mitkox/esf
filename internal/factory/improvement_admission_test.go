package factory

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/harnessprofile"
	"github.com/mitkox/esf/internal/inferencebudget"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

func TestImprovementAdmissionRegistersBoundedAccountsWithoutAllocatingVM(t *testing.T) {
	ctx := context.Background()
	ledger, e := inferencebudget.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer ledger.Close()
	admin, token := strings.Repeat("a", 32), strings.Repeat("t", 32)
	t.Setenv("H2_TEST_ADMIN", admin)
	t.Setenv("H2_TEST_TOKEN", token)
	pricing := "sha256:" + strings.Repeat("e", 64)
	route := inferencebudget.RouteDigest("https://provider.invalid/v1", "test-model", 1, 1, pricing)
	gateway := httptest.NewServer(&inferencebudget.Gateway{Ledger: ledger, AdminKey: admin, TokenKey: []byte(token), RouteDigest: route, PricingEvidence: pricing, InputPrice: 1, OutputPrice: 1, Model: "test-model"})
	defer gateway.Close()
	cfg := Default()
	cfg.Cube = cubeConfigForTest()
	cfg.Storage.DataDir = t.TempDir()
	cfg.Sandbox.BasePackages = nil
	h := cfg.Harnesses["opencode2"]
	h.Preinstalled = true
	h.Binary = "/opt/esf/agents/opencode2"
	h.BinarySHA256 = strings.Repeat("a", 64)
	h.CredentialMode = cubeEgressCredentialMode
	h.BaseURL = "https://provider.invalid/v1"
	h.APIKeyEnv = "SOURCE_KEY_NOT_READ_DURING_ADMISSION"
	h.CatalogCache = ""
	h.Packages = nil
	h.PassEnv = nil
	h.ProviderFiles = nil
	cfg.Harnesses["opencode2"] = h
	cfg.Models["budget"] = ModelConfig{Provider: "openai-compatible", Model: "fixture/test-model", BaseURL: "https://budget.invalid/v1", APIKeyEnv: "PLACEHOLDER"}
	cfg.Improvement = ImprovementConfig{GatewayControlURL: gateway.URL, GatewayPublicURL: "https://budget.invalid/v1", GatewayAdminKeyEnv: "H2_TEST_ADMIN", GatewayTokenKeyEnv: "H2_TEST_TOKEN", UpstreamURL: "https://provider.invalid/v1", WireModel: "test-model", RouteDigest: route, PricingEvidence: pricing, InputPrice: 1, OutputPrice: 1}
	p := ImprovementPolicy{Mode: "offline", Enabled: true, EditorHarness: "opencode2", Model: "budget", MaxCostMicros: 8000, MaxDuration: tomlx.FromStd(30 * time.Minute), CleanupAllowance: tomlx.FromStd(time.Minute), MaxInputTokens: 8192, MaxOutputTokens: 256, MaxRequests: 8}
	cfg.ImprovementPolicies = map[string]ImprovementPolicy{"offline": p}
	deny := false
	cfg.Egress = map[string]EgressPolicyConfig{"locked": {AllowInternet: &deny}}
	cfg.Scopes[DefaultScope] = ScopeConfig{Egress: "locked", ImprovementPolicies: []string{"offline"}}
	fake := sandbox.NewFake()
	runtime, e := NewRuntime(ctx, RuntimeOptions{Config: cfg, SandboxProvider: fake})
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close(ctx)
	a, e := runtime.Activities()
	if e != nil {
		t.Fatal(e)
	}
	req := baseRequest(t)
	req.AgentHarness = "opencode2"
	req.ImprovementPolicy = "offline"
	ad, e := a.AdmitImprovement(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if !ad.Enrolled || ad.Binding.Model != "fixture/test-model" || ad.Fingerprint == "" {
		t.Fatalf("bad admission %+v", ad)
	}
	if len(fake.Live()) != 0 || len(fake.Created()) != 0 {
		t.Fatal("admission allocated a VM")
	}
	var total int64
	for _, stage := range improvementStages {
		acct, e := ledger.Get(improvementID(req.RunID, stage))
		if e != nil {
			t.Fatal(e)
		}
		total += acct.LimitMicros
		if acct.Model != "test-model" || acct.MaxRequests != 1 {
			t.Fatal("wire model or stage quota wrong", acct)
		}
	}
	if total > p.MaxCostMicros {
		t.Fatal("stage accounts exceed parent limit")
	}
	// Verify that credentials are injected at the trusted proxy, and that the
	// ordinary harness route is not changed by this opt-in integration.
	n, _, e := cfg.improvementNetwork("opencode2", improvementID(req.RunID, "base"))
	if e != nil {
		t.Fatal(e)
	}
	if n.AllowsInternet() || len(n.Rules) != 1 || n.Rules[0].Match.Host != "budget.invalid" || n.Rules[0].Match.Path != "/v1/chat/completions" {
		t.Fatal("gateway egress is not restricted")
	}
	if cfg.Harnesses["opencode2"].BaseURL != "https://provider.invalid/v1" {
		t.Fatal("normal provider route changed")
	}
	if n.Rules[0].Action.Inject[0].Secret == token || n.Rules[0].Action.Inject[0].Secret == admin {
		t.Fatal("master credential exposed as a stage token")
	}
	small := a.cfg.ImprovementPolicies["offline"]
	small.MaxCostMicros = 1
	a.cfg.ImprovementPolicies["offline"] = small
	req.RunID = "cannot-fit"
	skipped, e := a.AdmitImprovement(ctx, req)
	if e != nil || skipped.Enrolled || !strings.Contains(skipped.Reason, "cannot fit") {
		t.Fatal("unaffordable round was not skipped", skipped, e)
	}
	if _, e = ledger.Get(improvementID(req.RunID, "base")); e == nil {
		t.Fatal("skipped round created accounts")
	}
	a.cfg.ImprovementPolicies["offline"] = p
	// A difficult task with no verified solution is a quality outcome, not a
	// security fault that disables the improvement policy for future tasks.
	finish := ImprovementFinish{Admission: ad, Receipt: ImprovementReceipt{Error: "no verified candidate available", FailureKind: "verification"}}
	retained, e := a.PersistImprovement(ctx, finish)
	if e != nil || retained.FailureKind != "verification" {
		t.Fatal(retained, e)
	}
	ps, e := cfg.ProfileStore()
	if e != nil {
		t.Fatal(e)
	}
	defer ps.Close()
	if _, e = ps.Read("circuits/"+p.Digest()[7:]+".json", harnessprofile.MaxArtifactBytes); e == nil {
		t.Fatal("ordinary verification failure suspended adaptation")
	}
	finish.Receipt = retained
	m, e := a.FinalizeImprovementFailure(ctx, finish)
	if e != nil || m.FactoryResult != StateVerificationFailed || m.Duration <= 0 || !m.CleanupResult.Verified || m.InferenceCost == nil {
		t.Fatal("failed-task evidence incomplete", m, e)
	}
	// Configuration changes stop the frozen execution before another stage.
	a.cfg.Limits.AgentTimeout = tomlx.FromStd(time.Second)
	if a.CheckImprovementAdmission(ctx, ad) == nil {
		t.Fatal("changed limits did not stop the experiment")
	}
}

func TestImprovementPolicyRejectsUnboundedLiveActivation(t *testing.T) {
	p := ImprovementPolicy{Mode: "adaptive", Profile: "profile", EditorHarness: "opencode2", Model: "budget", MaxCostMicros: 5000, BaselineCostMicros: 1000, BaselineDuration: tomlx.FromStd(5 * time.Minute), MaxDuration: tomlx.FromStd(25 * time.Minute), CleanupAllowance: tomlx.FromStd(time.Minute), MaxInputTokens: 8192, MaxOutputTokens: 1024, MaxRequests: 8, EnrollmentPercent: 1}
	if e := p.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*ImprovementPolicy){func(p *ImprovementPolicy) { p.MaxCostMicros++ }, func(p *ImprovementPolicy) { p.MaxDuration += tomlx.Duration(time.Second) }, func(p *ImprovementPolicy) { p.EnrollmentPercent = 6 }, func(p *ImprovementPolicy) { p.Profile = "" }} {
		q := p
		mutate(&q)
		if q.Validate() == nil {
			t.Fatal("unbounded live policy accepted", q)
		}
	}
	q := p
	q.Mode = "offline"
	q.Enabled = !p.Enabled
	q.EnrollmentPercent = 0
	if q.Digest() != p.Digest() {
		t.Fatal("activation mode changed the frozen improvement specification")
	}
}
