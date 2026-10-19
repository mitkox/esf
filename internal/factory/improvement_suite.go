package factory

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/harnessprofile"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type ImprovementFixture struct {
	ID      string     `json:"id"`
	Group   string     `json:"group"`
	Split   string     `json:"split"`
	Request RunRequest `json:"request"`
}
type ImprovementSuite struct {
	Schema   string               `json:"schema"`
	Fixtures []ImprovementFixture `json:"fixtures"`
}
type SuiteRequest struct {
	ID      string `json:"id"`
	Suite   string `json:"suite"`
	Policy  string `json:"policy"`
	Split   string `json:"split"`
	Compare bool   `json:"compare"`
	Repeats int    `json:"repeats"`
}
type SuiteAdmission struct {
	Digest        string   `json:"digest"`
	Fixtures      []string `json:"fixtures"`
	Fingerprint   string   `json:"fingerprint"`
	LibraryDigest string   `json:"library_digest"`
}
type FixtureRequest struct {
	SuiteRequest SuiteRequest   `json:"suite_request"`
	Admission    SuiteAdmission `json:"admission"`
	Fixture      string         `json:"fixture"`
}
type TrialRequest struct {
	Request  RunRequest `json:"request"`
	Strategy string     `json:"strategy"`
}
type SuiteResult struct {
	ID           string   `json:"id"`
	CorpusDigest string   `json:"corpus_digest"`
	Runs         []string `json:"runs"`
	Completed    bool     `json:"completed"`
}

func (c Config) ReadImprovementSuite(name string) (ImprovementSuite, string, error) {
	var s ImprovementSuite
	path, ok := c.ImprovementSuites[name]
	if !ok {
		return s, "", errors.New("unknown operator task suite")
	}
	i, e := os.Lstat(path)
	if e != nil || !i.Mode().IsRegular() {
		return s, "", errors.New("suite must be a regular operator-owned file")
	}
	f, e := os.Open(path)
	if e != nil {
		return s, "", e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if e != nil || len(b) > 4<<20 {
		return s, "", errors.New("suite exceeds limit")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = decodeOneImprovementJSON(d, &s); e != nil {
		return s, "", e
	}
	if s.Schema != "esf-improvement-suite/v1" || len(s.Fixtures) == 0 || len(s.Fixtures) > 1000 {
		return s, "", errors.New("invalid suite schema or size")
	}
	ids, tasks, groups, repositories := map[string]bool{}, map[string]bool{}, map[string]string{}, map[string]string{}
	for _, v := range s.Fixtures {
		if !harnessprofile.Name.MatchString(v.ID) || v.Group == "" || ids[v.ID] || (v.Split != "development" && v.Split != "validation" && v.Split != "qualification") {
			return s, "", errors.New("invalid fixture identity or split")
		}
		ids[v.ID] = true
		key := harnessprofile.Hash(strings.Join(strings.Fields(v.Request.Task), " "))
		if tasks[key] {
			return s, "", errors.New("duplicate normalized task across suite partitions")
		}
		tasks[key] = true
		repo := v.Request.Repository
		if repo == "" {
			repo = "local:" + v.Request.LocalPath
		}
		if split, ok := groups[v.Group]; ok && split != v.Split {
			return s, "", errors.New("task family crosses dataset partitions")
		}
		groups[v.Group] = v.Split
		if split, ok := repositories[repo]; ok && split != v.Split {
			return s, "", errors.New("repository lineage crosses dataset partitions")
		}
		repositories[repo] = v.Split
		if len(v.Request.Revision) != 40 || strings.Trim(v.Request.Revision, "0123456789abcdef") != "" || v.Request.AgentHarness != "opencode2" || v.Request.Task == "" {
			return s, "", errors.New("fixture requires pinned source and OpenCode2 task")
		}
	}
	return s, harnessprofile.Hash(s), nil
}
func (a *Activities) LoadImprovementSuite(_ context.Context, in SuiteRequest) (SuiteAdmission, error) {
	var out SuiteAdmission
	if !harnessprofile.Name.MatchString(in.ID) || in.Repeats < 1 || in.Repeats > 3 {
		return out, errors.New("invalid experiment ID or repeats")
	}
	p, ok := a.cfg.ImprovementPolicies[in.Policy]
	if !ok || p.Mode != "offline" {
		return out, errors.New("offline experiments require an offline policy")
	}
	if in.Compare && p.Profile == "" {
		return out, errors.New("static comparison requires a pinned offline candidate profile")
	}
	s, d, e := a.cfg.ReadImprovementSuite(in.Suite)
	if e != nil {
		return out, e
	}
	out.Digest = d
	out.Fingerprint = a.cfg.improvementFingerprint()
	out.LibraryDigest = harnessprofile.Hash([]string{a.cfg.HarnessProfiles[p.Profile]})
	for _, f := range s.Fixtures {
		if f.Split == in.Split {
			out.Fixtures = append(out.Fixtures, f.ID)
		}
	}
	if len(out.Fixtures) == 0 {
		return out, errors.New("suite partition is empty")
	}
	return out, nil
}
func (a *Activities) LoadImprovementFixture(_ context.Context, in FixtureRequest) (RunRequest, error) {
	s, d, e := a.cfg.ReadImprovementSuite(in.SuiteRequest.Suite)
	if e != nil {
		return RunRequest{}, e
	}
	if d != in.Admission.Digest || a.cfg.improvementFingerprint() != in.Admission.Fingerprint {
		return RunRequest{}, errors.New("experiment inputs changed; refusing incompatible resume")
	}
	for _, f := range s.Fixtures {
		if f.ID == in.Fixture && f.Split == in.SuiteRequest.Split {
			r := f.Request
			r.ImprovementPolicy = in.SuiteRequest.Policy
			return r, nil
		}
	}
	return RunRequest{}, errors.New("fixture is not in frozen partition")
}
func (a *Activities) PersistImprovementSuite(_ context.Context, in SuiteResult) error {
	s, e := a.artifactFactory.ForRun("experiment-" + in.ID)
	if e != nil {
		return e
	}
	return a.writeJSON(s, "experiment.json", in)
}
func ImprovementSuiteWorkflow(ctx workflow.Context, in SuiteRequest) (SuiteResult, error) {
	a := &Activities{}
	ac := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
	var ad SuiteAdmission
	if e := workflow.ExecuteActivity(ac, a.LoadImprovementSuite, in).Get(ctx, &ad); e != nil {
		return SuiteResult{}, e
	}
	out := SuiteResult{ID: in.ID, CorpusDigest: ad.Digest}
	arms := []string{"harness2"}
	if in.Compare {
		arms = []string{"baseline", "static", "harness2", "repeated-baseline"}
	}
	for _, fixture := range ad.Fixtures {
		var req RunRequest
		if e := workflow.ExecuteActivity(ac, a.LoadImprovementFixture, FixtureRequest{in, ad, fixture}).Get(ctx, &req); e != nil {
			return out, e
		}
		for repeat := 0; repeat < in.Repeats; repeat++ {
			for _, arm := range arms {
				child := req
				child.RunID = fmt.Sprintf("%s-%s-%d-%s", in.ID, harnessprofile.Hash(fixture)[7:19], repeat, arm)
				cc := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: WorkflowIDForRun(child.RunID), WaitForCancellation: true, ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 1}})
				var m RunManifest
				if e := workflow.ExecuteChildWorkflow(cc, ImprovementTrialWorkflow, TrialRequest{child, arm}).Get(ctx, &m); e != nil {
					return out, e
				}
				out.Runs = append(out.Runs, m.RunID)
				if e := workflow.ExecuteActivity(ac, a.PersistImprovementSuite, out).Get(ctx, nil); e != nil {
					return out, e
				}
			}
		}
	}
	out.Completed = true
	return out, workflow.ExecuteActivity(ac, a.PersistImprovementSuite, out).Get(ctx, nil)
}
func ImprovementTrialWorkflow(ctx workflow.Context, in TrialRequest) (RunManifest, error) {
	info := workflow.GetInfo(ctx)
	if info.ParentWorkflowExecution == nil || !strings.HasPrefix(info.ParentWorkflowExecution.ID, "factory-experiment-") {
		return RunManifest{}, errors.New("comparison trials require an experiment parent")
	}
	return improvementWorkflowStrategy(ctx, in.Request, in.Strategy)
}

// Rollout receipts authorize the 1% -> 5% transition and are signed separately
// from quality promotion. Operators retain the underlying pilot run evidence.
type RolloutReceipt struct {
	Policy    string    `json:"policy"`
	Scope     string    `json:"scope"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
	Tasks     int       `json:"tasks"`
	Evidence  string    `json:"evidence"`
	Reviewer  string    `json:"reviewer"`
	Expires   time.Time `json:"expires"`
	Signature string    `json:"signature"`
}

func (a *Activities) checkRolloutReceipt(p ImprovementPolicy, scope string) error {
	if !harnessprofile.DigestPattern.MatchString(p.RolloutReceipt) {
		return errors.New("expanded rollout requires a signed pilot receipt")
	}
	s, e := a.cfg.ProfileStore()
	if e != nil {
		return e
	}
	defer s.Close()
	b, e := s.Read("rollouts/"+p.RolloutReceipt[7:]+".json", 16384)
	if e != nil {
		return e
	}
	var r RolloutReceipt
	if e = json.Unmarshal(b, &r); e != nil {
		return e
	}
	if harnessprofile.Hash(r) != p.RolloutReceipt || r.Policy != p.Digest() || r.Scope != scope || r.Tasks < 100 || r.Last.Sub(r.First) < 7*24*time.Hour || r.Last.After(time.Now()) || !r.Expires.After(time.Now()) || !harnessprofile.DigestPattern.MatchString(r.Evidence) {
		return errors.New("pilot duration, task count or binding is insufficient")
	}
	sig, e := base64.StdEncoding.DecodeString(r.Signature)
	key, ke := base64.StdEncoding.DecodeString(a.cfg.Improvement.ApprovalKeys[r.Reviewer])
	r.Signature = ""
	if e != nil || ke != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, receiptBytes(r), sig) {
		return errors.New("pilot reviewer signature rejected")
	}
	return nil
}
