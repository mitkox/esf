package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/assurance"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
	"github.com/mitkox/esf/internal/verification"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
)

const qualityFixturePolicy = `apiVersion: esf.io/v1
kind: QualityPolicy
metadata: {name: qms, version: "1"}
spec:
  risk:
    default: R2
    rules:
      - {level: R0, paths: ["docs/**"], all_paths: true}
      - {level: R3, paths: ["auth/**"]}
  gates:
    - {id: tests, type: verification, profile: test, risks: [R1,R2,R3,R4]}
    - {id: review, type: independent-review, harness: reviewer, risks: [R2,R3,R4]}
  evidence: [patch, gate-result, review-report]
  qualifications:
    - {id: author-identity, phase: author, harness: author, model: fixture}
    - {id: reviewer-identity, phase: review, harness: reviewer, model: fixture}
  approvals:
    R0: {mode: automatic}
    R1: {mode: gates}
    R2: {mode: gates}
    R3: {mode: human, roles: [maintainer], count: 1, independent: true, timeout: 24h}
    R4: {mode: human, roles: [quality-authority], count: 2, independent: true, timeout: 24h}
`

// This provider executes only test-authored fixtures on the host. It substitutes
// actor processes and package installation, while exercising real Git transport,
// reconstruction, verification commands, content storage and Temporal activities.
type gitQualityProvider struct {
	*sandbox.Fake
	t               *testing.T
	mu              sync.Mutex
	instances       map[string]*gitQualitySandbox
	mutateReview    bool
	malformedReview bool
	authorPath      string
	renameFrom      string
}
type gitQualitySandbox struct {
	sandbox.Sandbox
	root     string
	provider *gitQualityProvider
}

func (p *gitQualityProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := p.Fake.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	root := p.t.TempDir()
	if err = os.MkdirAll(filepath.Join(root, "workspace"), 0700); err != nil {
		return nil, err
	}
	s := &gitQualitySandbox{sb, root, p}
	p.mu.Lock()
	p.instances[s.ID()] = s
	p.mu.Unlock()
	return s, nil
}
func (p *gitQualityProvider) Reattach(ctx context.Context, id string) (sandbox.Sandbox, error) {
	if _, err := p.Fake.Reattach(ctx, id); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.instances[id], nil
}
func (s *gitQualitySandbox) local(v string) string {
	return strings.ReplaceAll(v, "/workspace", filepath.Join(s.root, "workspace"))
}
func (s *gitQualitySandbox) WriteFile(_ context.Context, p string, b []byte) error {
	p = s.local(p)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0600)
}
func (s *gitQualitySandbox) ReadFile(_ context.Context, p string) ([]byte, error) {
	return os.ReadFile(s.local(p))
}
func (s *gitQualitySandbox) UpdateNetwork(ctx context.Context, n sandbox.Network) error {
	return s.Sandbox.(sandbox.NetworkUpdater).UpdateNetwork(ctx, n)
}
func (s *gitQualitySandbox) Execute(ctx context.Context, c sandbox.Command) (sandbox.Execution, error) {
	now := time.Now()
	result := sandbox.Execution{StartedAt: now, CompletedAt: now}
	if strings.HasPrefix(c.Description, "install ") || c.Description == "read agent version" {
		result.Stdout = "fixture-v1"
		return result, nil
	}
	if c.Description == "agent run (author)" {
		dir := s.local(DefaultRepositoryDir)
		name := s.provider.authorPath
		if name == "" {
			name = "committed.txt"
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0700); err != nil {
			return result, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("agent commit\n"), 0644); err != nil {
			return result, err
		}
		if s.provider.renameFrom != "" {
			if err := os.MkdirAll(filepath.Join(dir, "docs"), 0700); err != nil {
				return result, err
			}
			if err := os.Rename(filepath.Join(dir, s.provider.renameFrom), filepath.Join(dir, "docs/renamed.txt")); err != nil {
				return result, err
			}
		}
		runGit(s.provider.t, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@test", "add", "-A")
		runGit(s.provider.t, dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@test", "commit", "-qm", "agent commit")
		if err := os.WriteFile(filepath.Join(dir, "binary.bin"), []byte{0, 1, 255, 0, 13}, 0644); err != nil {
			return result, err
		}
		if err := os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			return result, err
		}
		if err := os.Symlink(name, filepath.Join(dir, "link")); err != nil {
			return result, err
		}
		if s.provider.renameFrom != "" {
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), nil, 0644); err != nil {
				return result, err
			}
			if err := os.WriteFile(filepath.Join(dir, "revealed.txt"), []byte("newly unignored"), 0644); err != nil {
				return result, err
			}
		}
		result.Stdout = "completed"
		return result, nil
	}
	if c.Description == "agent run (reviewer)" {
		prompt, err := s.ReadFile(ctx, "/workspace/.factory/review-prompt.txt")
		if err != nil {
			return result, err
		}
		digests := regexp.MustCompile(`sha256:[a-f0-9]{64}`).FindAllString(string(prompt), -1)
		if len(digests) < 2 {
			return result, fmt.Errorf("missing report bindings")
		}
		b, _ := json.Marshal(assurance.ReviewReport{CandidateDigest: digests[0], PlanDigest: digests[1], Verdict: "approve", Findings: []string{}})
		result.Stdout = string(b)
		if s.provider.malformedReview {
			result.Stdout = `{"verdict":"approve"}`
		}
		if s.provider.mutateReview {
			if err = s.WriteFile(ctx, DefaultRepositoryDir+"/committed.txt", []byte("reviewer mutation")); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	argv := append([]string{}, c.Argv...)
	for i := range argv {
		argv[i] = s.local(argv[i])
	}
	if len(argv) == 0 {
		argv = []string{"bash", "-c", s.local(c.Script)}
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = s.local(c.Dir)
	if command.Dir == "" {
		command.Dir = s.root
	}
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	for k, v := range c.Env {
		command.Env = append(command.Env, k+"="+s.local(v))
	}
	var out, stderr bytes.Buffer
	command.Stdout = &out
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			result.ExitCode = e.ExitCode()
		} else {
			return result, err
		}
	}
	result.Stdout = out.String()
	result.Stderr = stderr.String()
	result.CompletedAt = time.Now()
	return result, nil
}

func qualityRuntimeFixture(t *testing.T) (*Runtime, *gitQualityProvider, RunRequest) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("authenticated QMS worker requires Linux; standard factory tests run on all supported platforms")
	}
	repo := newFixtureRepo(t)
	cfg := Default()
	cfg.Cube = cubeConfigForTest()
	cfg.Storage.DataDir = t.TempDir()
	cfg.Harnesses = map[string]HarnessConfig{"author": {Type: "generic", Executable: "fixture-author", Model: "fixture", Timeout: tomlx.FromStd(time.Minute)}, "reviewer": {Type: "generic", Executable: "fixture-reviewer", Model: "fixture", Timeout: tomlx.FromStd(time.Minute)}}
	cfg.Verification = map[string]verification.Profile{"test": {Name: "test", Steps: []verification.Step{{ID: "contents", Argv: []string{"git", "diff", "--cached", "--check"}}}}}
	cfg.Sandbox.BasePackages = []string{"git"}
	cfg.Scopes = map[string]ScopeConfig{DefaultScope: {QualityPolicies: []string{"qms"}}, "alternate": {}}
	cfg.Quality = QualityConfig{PolicyFiles: []string{filepath.Join(t.TempDir(), "qms.yaml")}, Repositories: map[string]QualityRepository{"fixture": {LocalPaths: []string{repo}, Scopes: []string{DefaultScope, "alternate"}}}, Roles: map[string][]string{fmt.Sprint(os.Getuid()): {"submitter", "maintainer"}}}
	if err := os.WriteFile(cfg.Quality.PolicyFiles[0], []byte(qualityFixturePolicy), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &gitQualityProvider{Fake: sandbox.NewFake(), t: t, instances: map[string]*gitQualitySandbox{}}
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{Config: cfg, SandboxProvider: provider, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.openQuality(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close(context.Background()) })
	return runtime, provider, RunRequest{RunID: "quality-test", LocalPath: repo, Revision: fixtureSHA(t, repo), Task: "make a controlled fixture change", AgentHarness: "author", VerificationProfile: "test"}
}
func TestQualityRepositoryCannotBypassScopeOrSource(t *testing.T) {
	r, _, req := qualityRuntimeFixture(t)
	req.Scope = "alternate"
	required, err := r.Config.QualityRequired(req)
	if err != nil || !required {
		t.Fatalf("scope bypass: %v", err)
	}
	req.Repository = "https://example.invalid/controlled"
	req.RepositoryKind = "remote"
	if _, err = r.Config.QualityRequired(req); err == nil {
		t.Fatal("conflicting source fields accepted")
	}
	req.Repository = ""
	if _, err = r.Config.QualityRequired(req); err == nil {
		t.Fatal("source kind mismatch accepted")
	}
	req.RepositoryKind = ""
	req.LocalPath = t.TempDir()
	if _, err = r.Config.QualityRequired(req); err == nil {
		t.Fatal("unknown local path accepted")
	}
}
func TestQualityAdmissionAndFrozenDefinitions(t *testing.T) {
	ctx := context.Background()
	r, _, req := qualityRuntimeFixture(t)
	actor := assurance.Actor{ID: "uid:1", Kind: "human"}
	bad := req
	bad.SandboxTemplate = "unapproved"
	if _, err := r.admitQuality(ctx, actor, bad); err == nil {
		t.Fatal("unapproved template accepted")
	}
	record, err := r.admitQuality(ctx, actor, req)
	if err != nil {
		t.Fatal(err)
	}
	r.Config.Scopes = map[string]ScopeConfig{}
	retried, retryErr := r.admitQuality(ctx, actor, req)
	if retryErr != nil || retried.Snapshot.Digest != record.Snapshot.Digest {
		t.Fatalf("admission retry resolved changed configuration: %v", retryErr)
	}
	acts, err := r.Activities()
	if err != nil {
		t.Fatal(err)
	}
	var admitted RunRequest
	if err = json.Unmarshal(record.Request, &admitted); err != nil {
		t.Fatal(err)
	}
	admitted.AdmissionID = record.AdmissionID
	route, err := acts.QualityRoute(ctx, QualityRouteInput{admitted, record.WorkflowID, "exec"})
	if err != nil || !route.Controlled {
		t.Fatalf("live config changed admission: %v", err)
	}
	admitted.AdmissionID = "forged"
	if _, err = acts.QualityRoute(ctx, QualityRouteInput{admitted, record.WorkflowID, "exec"}); err == nil {
		t.Fatal("forged admission accepted")
	}
}

func TestQualityRejectsRedactionChangesAndUnsupportedSources(t *testing.T) {
	r, p, _ := qualityRuntimeFixture(t)
	acts, err := r.Activities()
	if err != nil {
		t.Fatal(err)
	}
	acts.redactor = NewRedactor("test-secret-with-newline\nvalue")
	for _, body := range [][]byte{[]byte("test-secret-with-newline\nvalue"), []byte(`{"secret":"test-secret-with-newline\nvalue"}`)} {
		if _, err = acts.qualityEvidence("patch", body, "factory", "", "", "factory-observed", false); err == nil {
			t.Fatal("changed authoritative bytes accepted")
		}
	}
	ctx := context.Background()
	sb, err := p.Create(ctx, sandbox.Spec{Template: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".gitmodules", ".gitattributes"} {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q")
		body := []byte("unsupported")
		if name == ".gitattributes" {
			body = []byte("*.bin filter=lfs diff=lfs merge=lfs -text\n")
		}
		if err = os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "add", "-A")
		g := sb.(*gitQualitySandbox)
		target := g.local(DefaultRepositoryDir)
		if err = os.RemoveAll(target); err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(dir, target); err != nil {
			t.Fatal(err)
		}
		if err = qualitySourceCheck(ctx, sb); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}
func TestQualityWorkflowReconstructsCommittedAndUntrackedBytes(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		reviewMutation, badReport, human, rename bool
	}{{"R2", false, false, false, false}, {"R3", false, false, true, false}, {"review-mutation", true, false, false, false}, {"malformed-review", false, true, false, false}, {"rename-sensitive", false, false, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r, p, req := qualityRuntimeFixture(t)
			p.mutateReview = tc.reviewMutation
			p.malformedReview = tc.badReport
			if tc.human {
				p.authorPath = "auth/committed.txt"
			}
			if tc.rename {
				p.authorPath = ""
				p.renameFrom = "auth/original.go"
				if err := os.MkdirAll(filepath.Join(req.LocalPath, "auth"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(req.LocalPath, p.renameFrom), []byte("sensitive source"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(req.LocalPath, ".gitignore"), []byte("revealed.txt\n"), 0644); err != nil {
					t.Fatal(err)
				}
				runGit(t, req.LocalPath, "add", "-A")
				runGit(t, req.LocalPath, "commit", "-qm", "sensitive baseline")
				req.Revision = fixtureSHA(t, req.LocalPath)
			}
			record, err := r.admitQuality(ctx, assurance.Actor{ID: "uid:1", Kind: "human", Roles: []string{"submitter"}}, req)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(record.Request, &req); err != nil {
				t.Fatal(err)
			}
			req.AdmissionID = record.AdmissionID
			acts, err := r.Activities()
			if err != nil {
				t.Fatal(err)
			}
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: record.WorkflowID})
			env.RegisterWorkflow(SoftwareChangeWorkflow)
			env.RegisterActivity(acts)
			if tc.human {
				env.RegisterDelayedCallback(func() {
					current, e := r.Quality.Store.Get(ctx, req.RunID)
					if e != nil {
						t.Error(e)
						return
					}
					if current.Approval == nil {
						t.Error("approval request missing")
						return
					}
					if len(p.Live()) != 0 {
						t.Error("live compute during approval")
					}
					_, e = r.Quality.Store.Approve(ctx, req.RunID, assurance.Actor{ID: "uid:2", Kind: "human", Roles: []string{"maintainer"}}, assurance.ApprovalInput{Operation: "approve", RequestID: current.Approval.ID, CandidateDigest: current.Candidate.Digest, PlanDigest: current.Plan.Digest, Decision: "approve", Reason: "verified fixture"})
					if e != nil {
						t.Error(e)
					}
					env.SignalWorkflow(QualityWakeSignal, nil)
				}, 10*time.Second)
			}
			env.ExecuteWorkflow(SoftwareChangeWorkflow, req)
			var m RunManifest
			if err = env.GetWorkflowResult(&m); err != nil {
				t.Fatal(err)
			}
			want := StateSucceeded
			if tc.reviewMutation || tc.badReport {
				want = StateQualityFailed
			}
			if m.FactoryResult != want {
				t.Fatalf("state=%s expected=%s reason=%s", m.FactoryResult, want, m.Error)
			}
			if len(p.Live()) != 0 {
				t.Fatal("sandbox leak")
			}
			record, err = r.Quality.Store.Get(ctx, req.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if record.Candidate == nil {
				t.Fatal("no candidate")
			}
			patch, err := r.Quality.Content.Read(record.Candidate.PatchDigest)
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range []string{"committed.txt", "GIT binary patch", "new file mode 100755", "new file mode 120000"} {
				if !bytes.Contains(patch, []byte(marker)) {
					t.Errorf("patch missing %s", marker)
				}
			}
			if tc.rename {
				for _, name := range []string{"auth/original.go", "docs/renamed.txt", ".gitignore", "revealed.txt"} {
					if !assurance.Contains(record.Candidate.Paths, name) {
						t.Errorf("risk paths lost %s", name)
					}
				}
				if record.Plan.Risk != "R3" {
					t.Fatal("sensitive rename lowered risk")
				}
			}
			if len(record.Gates) != 2 {
				t.Fatalf("gates: %+v", record.Gates)
			}
			if len(p.Created()) != 4 {
				t.Fatalf("expected distinct author, reconstruction, verifier and reviewer: %v", p.Created())
			}
		})
	}
}
