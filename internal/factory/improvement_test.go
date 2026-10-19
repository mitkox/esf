package factory

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/harnessprofile"
	"github.com/mitkox/esf/internal/inferencebudget"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestImprovementSelectionKeepsVerificationAuthority(t *testing.T) {
	for _, tc := range []struct {
		name          string
		composedState RunState
		patch         string
		scope         string
		want          string
	}{
		{"verified composition", StateSucceeded, "patch", "scope", "composed"},
		{"failed composition", StateVerificationFailed, "patch", "scope", "base"},
		{"empty composition", StateSucceeded, "", "scope", "base"},
		{"cross-scope composition", StateSucceeded, "patch", "other", "base"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := artifacts.NewLocal(t.TempDir())
			a := &Activities{artifactFactory: store}
			ad := ImprovementAdmission{Request: RunRequest{RunID: "parent", Revision: testSHA, AgentHarness: "opencode2"}, Binding: harnessprofile.Binding{Scope: "scope", Repository: "repo", Model: "model"}, Policy: ImprovementPolicy{Mode: "adaptive"}}
			for _, stage := range []string{"base", "composed"} {
				id := improvementID("parent", stage)
				s, _ := store.ForRun(id)
				m := RunManifest{RunID: id, FactoryResult: StateSucceeded, VerificationResult: OutcomeSuccess, CleanupResult: CleanupResult{Verified: true}, BaselineSHA: testSHA, Scope: "scope", Repository: "repo", Model: "model", AgentHarness: "opencode2", Patch: ArtifactPatch}
				patch := "baseline patch"
				if stage == "composed" {
					m.FactoryResult = tc.composedState
					m.Scope = tc.scope
					patch = tc.patch
				}
				s.Write(ArtifactPatch, []byte(patch))
				s.Write(ArtifactRun, receiptBytes(m))
			}
			r, e := a.SelectImprovement(context.Background(), ImprovementFinish{Admission: ad})
			if e != nil || r.Error != "" || r.SelectedRun != improvementID("parent", tc.want) {
				t.Fatalf("selection=%+v error=%v", r, e)
			}
			ad.Policy.Mode = "shadow"
			r, e = a.SelectImprovement(context.Background(), ImprovementFinish{Admission: ad})
			if e != nil || r.SelectedRun != improvementID("parent", "base") {
				t.Fatal("shadow changed the deliverable")
			}
		})
	}
}

func TestProbeEntryCannotBypassReview(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(ImprovementProbeWorkflow, ProbeRequest{Admission: ImprovementAdmission{Request: RunRequest{RunID: "parent"}}})
	if env.GetWorkflowError() == nil {
		t.Fatal("top-level probe accepted")
	}
}

func TestNoVerifiedImprovementIsAQualityFailure(t *testing.T) {
	store, _ := artifacts.NewLocal(t.TempDir())
	a := &Activities{artifactFactory: store}
	ad := ImprovementAdmission{Request: RunRequest{RunID: "parent"}, Policy: ImprovementPolicy{Mode: "adaptive"}}
	for _, stage := range []string{"base", "composed"} {
		s, _ := store.ForRun(improvementID("parent", stage))
		s.Write(ArtifactRun, receiptBytes(RunManifest{FactoryResult: StateVerificationFailed}))
	}
	r, e := a.SelectImprovement(context.Background(), ImprovementFinish{Admission: ad})
	if e != nil || r.SelectedRun != "" || r.Error == "" || r.FailureKind != "verification" {
		t.Fatal("ordinary failed gates were treated as an infrastructure fault", r, e)
	}
}

func runImprovementReviewTest(t *testing.T, decision string) {
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	req := baseRequest(t)
	req.ImprovementPolicy = "pilot"
	parent := req.RunID
	var children, sessions []string
	m, e := runWorkflowOpts(t, fake, req, workflowRunOptions{mutateConfig: func(c *Config) { c.Review.Enabled = true; c.Review.Timeout = tomlx.FromStd(time.Minute) }}, func(env *testsuite.TestWorkflowEnvironment) {
		a := &Activities{}
		ad := ImprovementAdmission{Request: req, PolicyName: "pilot", Enrolled: true, Deadline: time.Now().Add(time.Hour), Policy: ImprovementPolicy{Mode: "adaptive", MaxDuration: tomlx.FromStd(time.Hour), CleanupAllowance: tomlx.FromStd(time.Minute)}}
		env.RegisterWorkflow(ImprovementProbeWorkflow)
		env.OnActivity(a.AdmitImprovement, mock.Anything, mock.Anything).Return(ad, nil)
		env.OnWorkflow(ImprovementProbeWorkflow, mock.Anything, mock.Anything).Return(func(_ workflow.Context, in ProbeRequest) (RunManifest, error) {
			children = append(children, in.Stage)
			return RunManifest{RunID: improvementID(parent, in.Stage), CleanupResult: CleanupResult{Verified: true}, FactoryResult: StateSucceeded}, nil
		})
		env.OnActivity(a.RunImprovementSession, mock.Anything, mock.Anything).Return(func(_ context.Context, in SessionRequest) (SessionResult, error) {
			sessions = append(sessions, in.Stage)
			return SessionResult{Profile: harnessprofile.Hash(in.Stage)}, nil
		})
		env.OnActivity(a.SelectImprovement, mock.Anything, mock.Anything).Return(ImprovementReceipt{SelectedRun: improvementID(parent, "composed")}, nil)
		env.OnActivity(a.PersistImprovement, mock.Anything, mock.Anything).Return(func(_ context.Context, in ImprovementFinish) (ImprovementReceipt, error) { return in.Receipt, nil })
		env.OnActivity(a.CleanupImprovement, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(a.ApplyRuntimeNetwork, mock.Anything, mock.Anything).Return(ApplyRuntimeNetworkOutput{Probe: &EgressProbeResult{Canary: ProbeObservation{Decided: true}, Metadata: ProbeObservation{Decided: true}}}, nil)
		env.OnActivity(a.RunAgent, mock.Anything, mock.Anything).Return(RunAgentOutput{Result: agentharness.Result{Harness: "test-agent", ExitCode: 0}}, nil)
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(ReviewSignalName, ReviewDecision{Decision: decision, Actor: "fixture-reviewer", Note: "fixture decision"})
		}, time.Second)
	})
	if e != nil {
		t.Fatal(e)
	}
	expected := StateSucceeded
	if decision == HumanResultRejected {
		expected = StateRejected
	}
	if m.FactoryResult != expected || m.Improvement == nil || m.HumanResult != decision {
		t.Fatalf("final result %+v", m)
	}
	if len(children) != 4 || children[0] != "base" || children[3] != "composed" || !((children[1] == "general-probe" && children[2] == "domain-probe") || (children[1] == "domain-probe" && children[2] == "general-probe")) {
		t.Fatal("unexpected solver executions", children)
	}
	if strings.Join(sessions, ",") != "general,domain,compose,reflect" {
		t.Fatal("unexpected editor sessions", sessions)
	}
}

func TestImprovementWorkflowRunsFourTasksAndRetainsReview(t *testing.T) {
	runImprovementReviewTest(t, HumanResultApproved)
}
func TestImprovementCannotOverrideHumanRejection(t *testing.T) {
	runImprovementReviewTest(t, HumanResultRejected)
}

func TestInstructionProfileRequiresScopeAndFrozenRuntime(t *testing.T) {
	cfg := Config{Storage: StorageConfig{DataDir: t.TempDir()}, Harnesses: map[string]HarnessConfig{"opencode2": {Type: "opencode", BaseURL: "https://provider.invalid/v1", Preinstalled: true, CredentialMode: cubeEgressCredentialMode, BinarySHA256: strings.Repeat("a", 64)}}, HarnessProfiles: map[string]string{}, Scopes: map[string]ScopeConfig{"scope": {HarnessProfiles: []string{"reviewed"}}}}
	cfg.Improvement = ImprovementConfig{UpstreamURL: "https://provider.invalid/v1", WireModel: "model", InputPrice: 1, OutputPrice: 1, PricingEvidence: harnessprofile.Hash("pricing")}
	cfg.Improvement.RouteDigest = inferencebudget.RouteDigest(cfg.Improvement.UpstreamURL, "model", 1, 1, cfg.Improvement.PricingEvidence)
	p := harnessprofile.Profile{Schema: harnessprofile.Schema, Binding: harnessprofile.Binding{Scope: "scope", Repository: "repo", Harness: "opencode2", Model: "model", HarnessDigest: "sha256:" + strings.Repeat("a", 64), Template: "template"}, Author: "editor", Upstream: harnessprofile.Upstream, Components: map[string]string{"systemprompt.md": "Inspect and verify the original task."}}
	s, _ := cfg.ProfileStore()
	defer s.Close()
	d, e := s.Put(p)
	if e != nil {
		t.Fatal(e)
	}
	cfg.HarnessProfiles["reviewed"] = d
	a := &Activities{cfg: cfg}
	req := RunRequest{HarnessProfile: "reviewed", AgentHarness: "opencode2", Repository: "repo", AgentModel: "model"}
	res := ResolvedResources{Scope: "scope", WorkspaceConfig: WorkspaceConfig{Template: "template"}}
	if a.resolveInstructionProfile(req, &res) == nil {
		t.Fatal("unapproved profile selected")
	}
	in := harnessprofile.QualityInput{Split: "qualification", Profile: d, Binding: p.Binding, Mode: "static", SourceCommit: testSHA, CorpusDigest: harnessprofile.Hash("corpus"), LibraryDigest: harnessprofile.Hash("library"), RouteDigest: cfg.Improvement.RouteDigest}
	for i := 0; i < 100; i++ {
		for repeat := 0; repeat < 3; repeat++ {
			for _, arm := range []string{"baseline", "candidate"} {
				accepted := i%10 < 4
				if arm == "candidate" {
					accepted = i%10 < 6
				}
				cost := int64(100)
				in.Trials = append(in.Trials, harnessprofile.Trial{Task: fmt.Sprint(i), Group: fmt.Sprint(i / 10), Repeat: repeat, Arm: arm, Verified: accepted, Accepted: accepted, CostMicros: &cost, DurationMillis: 100, Evidence: []string{harnessprofile.Hash(fmt.Sprint(i, repeat, arm))}})
			}
		}
	}
	report := harnessprofile.Qualify(in)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cfg.Improvement.ApprovalKeys = map[string]string{"reviewer": base64.StdEncoding.EncodeToString(pub)}
	approval := harnessprofile.Approval{Schema: "esf-harness-approval/v1", Reviewer: "reviewer", Profile: d, Qualification: harnessprofile.Hash(report), Binding: p.Binding, Expires: time.Now().Add(time.Hour)}
	approval.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, approval.SigningBytes()))
	if e = s.Promote(p, report, approval, cfg.Improvement.ApprovalKeys, time.Now()); e != nil {
		t.Fatal(e)
	}
	a.cfg = cfg
	if e = a.resolveInstructionProfile(req, &res); e != nil {
		t.Fatal(e)
	}
	if res.HarnessProfileDigest != d {
		t.Fatal("digest not pinned")
	}
	hc := a.cfg.Harnesses["opencode2"]
	hc.BaseURL = "https://different.invalid/v1"
	a.cfg.Harnesses["opencode2"] = hc
	if a.resolveInstructionProfile(req, &res) == nil {
		t.Fatal("changed provider route activated the profile")
	}
	hc.BaseURL = cfg.Improvement.UpstreamURL
	a.cfg.Harnesses["opencode2"] = hc
	res.WorkspaceConfig.Template = "different"
	if a.resolveInstructionProfile(req, &res) == nil {
		t.Fatal("profile activated on a different runtime")
	}
	res.Scope = "other"
	if a.resolveInstructionProfile(req, &res) == nil {
		t.Fatal("profile crossed tenancy")
	}
}

func TestSuiteRejectsContaminatedPartitions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suite.json")
	cfg := Config{ImprovementSuites: map[string]string{"suite": path}}
	fixture := ImprovementFixture{ID: "first", Group: "family", Split: "development", Request: RunRequest{Repository: "repo", Revision: testSHA, Task: "fix a bug", AgentHarness: "opencode2"}}
	second := fixture
	second.ID = "second"
	second.Split = "qualification"
	second.Request.Task = "implement another feature"
	b, _ := json.Marshal(ImprovementSuite{Schema: "esf-improvement-suite/v1", Fixtures: []ImprovementFixture{fixture, second}})
	os.WriteFile(path, b, 0600)
	if _, _, e := cfg.ReadImprovementSuite("suite"); e == nil {
		t.Fatal("family crossed partitions")
	}
	second.Group = "other-family"
	second.Request.Task = "  fix   a bug  "
	b, _ = json.Marshal(ImprovementSuite{Schema: "esf-improvement-suite/v1", Fixtures: []ImprovementFixture{fixture, second}})
	os.WriteFile(path, b, 0600)
	if _, _, e := cfg.ReadImprovementSuite("suite"); e == nil {
		t.Fatal("normalized duplicate crossed partitions")
	}
}
