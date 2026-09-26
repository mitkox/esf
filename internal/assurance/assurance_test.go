package assurance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPolicy = `apiVersion: esf.io/v1
kind: QualityPolicy
metadata: {name: qms, version: "1"}
spec:
  risk:
    default: R2
    rules:
      - {level: R0, paths: ["docs/**", "*.md"], all_paths: true}
      - {level: R1, paths: ["assets/**"], all_paths: true}
      - {level: R3, paths: ["auth/**"]}
      - {level: R4, paths: ["safety/**"]}
  gates:
    - {id: tests, type: verification, profile: test, risks: [R1, R2, R3, R4]}
  evidence: [patch]
  approvals:
    R0: {mode: automatic}
    R1: {mode: gates}
    R2: {mode: gates}
    R3: {mode: human, roles: [maintainer], count: 1, independent: true, timeout: 24h}
    R4: {mode: human, roles: [quality-authority], count: 2, independent: true, timeout: 24h}
  exceptions:
    - {controls: [tests], roles: [quality-authority]}
`

func policyFixture(t *testing.T) Policy {
	t.Helper()
	p, err := ParsePolicy([]byte(testPolicy))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPolicyRiskAndUnion(t *testing.T) {
	p := policyFixture(t)
	for _, tc := range []struct {
		paths []string
		risk  string
	}{{[]string{"docs/a.md", "README.md"}, "R0"}, {[]string{"assets/logo.svg"}, "R1"}, {[]string{"README.md", "server.go"}, "R2"}, {[]string{"auth/old.go", "docs/new.md"}, "R3"}, {[]string{"safety/core.go", "auth/auth.go"}, "R4"}, {nil, "R2"}} {
		plan, err := Evaluate(Snapshot{Policies: []Policy{p}}, tc.paths)
		if err != nil || plan.Risk != tc.risk {
			t.Fatalf("paths %v: %+v %v", tc.paths, plan, err)
		}
	}
	p.Spec.Risk.Floor = "R3"
	plan, err := Evaluate(Snapshot{Policies: []Policy{p}}, []string{"docs/a.md"})
	if err != nil || plan.Risk != "R3" || len(plan.Controls) != 1 {
		t.Fatalf("floor: %+v %v", plan, err)
	}
	p2 := policyFixture(t)
	p2.Metadata.Name = "additional"
	p2.Spec.Gates[0].Profile = "conflicting"
	if _, err = Evaluate(Snapshot{Policies: []Policy{p, p2}}, []string{"server.go"}); err == nil {
		t.Fatal("accepted conflicting definitions")
	}
}
func TestPolicyStrictValidation(t *testing.T) {
	for _, body := range []string{testPolicy + "unknown: true\n", strings.Replace(testPolicy, "profile: test", "profile: test, command: evil", 1), strings.Replace(testPolicy, "default: R2", "default: R5", 1), testPolicy + "---\n{}", strings.Replace(testPolicy, "controls: [tests]", "controls: [identity]", 1)} {
		if _, err := ParsePolicy([]byte(body)); err == nil {
			t.Fatalf("accepted invalid policy %s", body)
		}
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func reserveFixture(t *testing.T, s *Store, id string) Run {
	t.Helper()
	p := policyFixture(t)
	snapshot := Snapshot{Evaluator: EvaluatorVersion, RepositoryID: "repo", Policies: []Policy{p}, Dependencies: json.RawMessage(`{}`)}
	snapshot.Digest = Hash(snapshot)
	r, err := s.Reserve(context.Background(), Run{ID: id, AdmissionID: "token", RequestDigest: Digest([]byte(id)), Request: json.RawMessage(`{}`), Requester: Actor{ID: "uid:1", UID: 1, Kind: "human", Roles: []string{"submitter"}}, Snapshot: snapshot, WorkflowID: "factory-run-" + id})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func candidateFixture(t *testing.T, s *Store, c *Content, id, riskPath string, pass bool) Run {
	t.Helper()
	r := reserveFixture(t, s, id)
	patch := []byte("exact patch")
	digest, err := c.Put(patch)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Evaluate(r.Snapshot, []string{riskPath})
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{RepositoryID: "repo", PatchDigest: digest, Paths: []string{riskPath}}
	candidate.Digest = Hash(candidate)
	r, err = s.Transition(context.Background(), id, "candidate", candidate, func(r *Run, _ time.Time) error {
		r.Candidate = &candidate
		r.Plan = &plan
		r.Evidence = []Evidence{{Kind: "patch", Digest: digest, Size: int64(len(patch)), CandidateDigest: candidate.Digest, Origin: "factory-observed"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Controls) > 0 {
		status := "failed"
		if pass {
			status = "passed"
		}
		r, err = s.RecordGate(context.Background(), id, GateAttempt{ID: "tests-attempt-1", ControlID: "tests", CandidateDigest: candidate.Digest, PlanDigest: plan.Digest, Status: status, Executor: Actor{ID: "factory", Kind: "factory"}, Reason: "test result"})
		if err != nil {
			t.Fatal(err)
		}
	}
	return r
}
func newContent(t *testing.T) *Content {
	t.Helper()
	c, err := OpenContent(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func approvalInput(r Run, op, decision string) ApprovalInput {
	return ApprovalInput{Operation: op, RequestID: r.Approval.ID, CandidateDigest: r.Candidate.Digest, PlanDigest: r.Plan.Digest, Decision: decision, Reason: "reviewed exact candidate"}
}
func TestAdmissionIdempotencyAndPolicyImmutability(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	r := reserveFixture(t, s, "one")
	if _, err := s.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.RequestDigest = "different"
	if _, err := s.Reserve(ctx, r); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	r.ID = "two"
	r.Snapshot.Policies[0].Metadata.Description = "changed"
	if _, err := s.Reserve(ctx, r); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutable policy: %v", err)
	}
	if _, err := s.Bind(ctx, "one", "forged", Digest([]byte("one")), "factory-run-one", "exec"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := s.Bind(ctx, "one", "token", Digest([]byte("one")), "factory-run-one", "exec"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Bind(ctx, "one", "token", Digest([]byte("one")), "factory-run-one", "other"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestApprovalIdentityQuorumAndDeadline(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := newContent(t)
	r := candidateFixture(t, s, c, "approval", "safety/core.go", true)
	r, err := s.OpenApproval(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	self := r.Requester
	self.Roles = []string{"quality-authority"}
	if _, err = s.Approve(ctx, r.ID, self, approvalInput(r, "self", "approve")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self approval: %v", err)
	}
	actor := Actor{ID: "uid:2", Kind: "human", Roles: []string{"quality-authority"}}
	r, err = s.Approve(ctx, r.ID, actor, approvalInput(r, "first", "approve"))
	if err != nil || r.Approval.Status != "pending" {
		t.Fatalf("quorum: %+v %v", r.Approval, err)
	}
	if _, err = s.Approve(ctx, r.ID, actor, approvalInput(r, "duplicate", "approve")); err == nil {
		t.Fatal("duplicate actor counted")
	}
	actor.ID = "uid:3"
	r, err = s.Approve(ctx, r.ID, actor, approvalInput(r, "second", "approve"))
	if err != nil || r.Approval.Status != "approved" {
		t.Fatal(err)
	}
	if _, err = s.Approve(ctx, r.ID, actor, approvalInput(r, "late", "reject")); !errors.Is(err, ErrTerminal) {
		t.Fatalf("terminal overwritten: %v", err)
	}
	expired := candidateFixture(t, s, c, "expired", "auth/a.go", true)
	expired, err = s.OpenApproval(ctx, expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return expired.Approval.Deadline }
	actor.Roles = []string{"maintainer"}
	if _, err = s.Approve(ctx, expired.ID, actor, approvalInput(expired, "at-deadline", "approve")); err == nil {
		t.Fatal("accepted deadline equality")
	}
	expired, err = s.ResolveApproval(ctx, expired.ID)
	if err != nil || expired.Approval.Status != "expired" {
		t.Fatal(err)
	}
}
func TestApprovalConcurrentFirstTerminalWins(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	r := candidateFixture(t, s, newContent(t), "race", "auth/a.go", true)
	r, err := s.OpenApproval(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, decision := range []string{"approve", "reject"} {
		wg.Add(1)
		go func(i int, d string) {
			defer wg.Done()
			_, e := s.Approve(ctx, r.ID, Actor{ID: []string{"uid:2", "uid:3"}[i], Kind: "human", Roles: []string{"maintainer"}}, approvalInput(r, d, d))
			results <- e
		}(i, decision)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrTerminal) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("%d terminal decisions", success)
	}
}
func TestExceptionPreservesFailureAndCannotWaiveEvidence(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := newContent(t)
	r := candidateFixture(t, s, c, "waiver", "server.go", false)
	r, err := s.OpenApproval(ctx, r.ID)
	if err != nil || r.Approval.Status != "pending" {
		t.Fatalf("no disposition window: %v", err)
	}
	in := ExceptionInput{Operation: "waive", ControlID: "tests", CandidateDigest: r.Candidate.Digest, PlanDigest: r.Plan.Digest, Reason: "accepted bounded deviation"}
	actor := Actor{ID: "uid:2", Kind: "human", Roles: []string{"quality-authority"}}
	r, err = s.GrantException(ctx, r.ID, actor, in)
	if err != nil || r.Approval.Status != "approved" || len(MissingControls(r)) != 0 {
		t.Fatalf("waiver: %+v %v", r, err)
	}
	if r.Gates[0].Status != "failed" || len(r.NonConformances) != 1 {
		t.Fatal("failure history lost")
	}
	r.Evidence = nil
	if len(MissingControls(r)) != 1 {
		t.Fatal("waived missing evidence")
	}
}
func TestFinalCommitRestartAndRepairableExports(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "quality.db")
	s, err := OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	c := newContent(t)
	r := candidateFixture(t, s, c, "final", "server.go", true)
	if _, err = s.OpenApproval(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	manifest := func(r Run, d Decision, digest string) (json.RawMessage, error) {
		return json.Marshal(map[string]any{"result": d, "attestation": digest})
	}
	r, err = s.CommitResult(ctx, r.ID, c, manifest, "", true)
	if err != nil || r.Decision.Result != "approved" {
		t.Fatalf("commit: %+v %v", r, err)
	}
	saved := string(r.Manifest)
	att := string(r.Attestation)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err = s.CommitResult(ctx, r.ID, c, func(Run, Decision, string) (json.RawMessage, error) {
		t.Fatal("manifest rebuilt after commit")
		return nil, nil
	}, "different retry", false)
	if err != nil || string(r.Manifest) != saved || string(r.Attestation) != att {
		t.Fatal("terminal bytes changed", err)
	}
	pending, err := s.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exports := 0
	for _, o := range pending {
		if o.Kind == "export-run" {
			exports++
			if err = s.CompleteOutbox(ctx, o.ID, "disk full"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if exports != 1 {
		t.Fatal("missing durable export")
	}
	if _, err = s.Transition(ctx, r.ID, "modify", "bad", func(r *Run, _ time.Time) error { r.Decision = nil; return nil }); !errors.Is(err, ErrTerminal) {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("DELETE FROM quality_events"); err == nil {
		t.Fatal("audit mutable")
	}
}
func TestFinalizationRollbackIntegrityAndUniqueNC(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	c := newContent(t)
	r := candidateFixture(t, s, c, "rollback", "server.go", false)
	_, err := s.CommitResult(ctx, r.ID, c, func(Run, Decision, string) (json.RawMessage, error) {
		return nil, errors.New("injected crash before commit")
	}, "", true)
	if err == nil {
		t.Fatal("missing injected failure")
	}
	r, _ = s.Get(ctx, r.ID)
	if r.Decision != nil {
		t.Fatal("partial commit")
	}
	r, err = s.CommitResult(ctx, r.ID, c, func(Run, Decision, string) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }, "", true)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, nc := range r.NonConformances {
		if nc.ControlID == "tests" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate control NC")
	}
	if len(r.Attestation) > 0 {
		t.Fatal("attested failed run")
	}
	r = candidateFixture(t, s, c, "corrupt", "docs/a.md", true)
	if _, err = s.OpenApproval(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	p, _ := c.path(r.Candidate.PatchDigest)
	if err = os.WriteFile(p, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = s.CommitResult(ctx, r.ID, c, func(Run, Decision, string) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }, "", true)
	if err != nil || r.Decision.Result != "rejected" {
		t.Fatalf("corrupt content approved: %v", err)
	}
}
func TestEvidenceOriginAndCompletedFailureCannotBeRetriedAway(t *testing.T) {
	s := newTestStore(t)
	r := candidateFixture(t, s, newContent(t), "persistent", "server.go", false)
	g := r.Gates[0]
	g.ID = "tests-attempt-2"
	g.Status = "passed"
	r, err := s.RecordGate(context.Background(), r.ID, g)
	if err != nil {
		t.Fatal(err)
	}
	if !Contains(MissingControls(r), "control:tests") {
		t.Fatal("later pass erased completed failure")
	}
}

func TestExceptionDoesNotShortenHumanApprovalBudget(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	r := candidateFixture(t, s, newContent(t), "two-clocks", "auth/a.go", false)
	r, err := s.OpenApproval(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := r.Approval.Deadline
	r, err = s.GrantException(ctx, r.ID, Actor{ID: "quality", Kind: "human", Roles: []string{"quality-authority"}}, ExceptionInput{Operation: "waive", ControlID: "tests", CandidateDigest: r.Candidate.Digest, PlanDigest: r.Plan.Digest, Reason: "bounded deviation"})
	if err != nil {
		t.Fatal(err)
	}
	if ApprovalDeadline(r) != deadline {
		t.Fatal("disposition shortened human deadline")
	}
	s.Now = func() time.Time { return r.Approval.RequestedAt.Add(2 * time.Hour) }
	r, err = s.Approve(ctx, r.ID, Actor{ID: "maintainer", Kind: "human", Roles: []string{"maintainer"}}, approvalInput(r, "approve", "approve"))
	if err != nil || r.Approval.Status != "approved" {
		t.Fatal(err)
	}
}

func TestIntegrityControlCannotBeWaivedByGateException(t *testing.T) {
	s := newTestStore(t)
	r := candidateFixture(t, s, newContent(t), "integrity", "server.go", true)
	g := r.Gates[0]
	g.ID = "integrity-receipt"
	g.Status = "failed"
	g.NonWaivable = true
	g.Reason = "source mutation"
	if _, err := s.RecordGate(context.Background(), r.ID, g); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenApproval(context.Background(), r.ID); err == nil {
		t.Fatal("opened a disposition window for integrity failure")
	}
}

func TestPolicyExceptionCannotWeakenAnotherPolicy(t *testing.T) {
	a := policyFixture(t)
	b := policyFixture(t)
	b.Metadata.Name = "stricter"
	b.Spec.Exceptions = nil
	plan, err := Evaluate(Snapshot{Policies: []Policy{a, b}}, []string{"server.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Exceptions) != 0 {
		t.Fatal("permissive pack weakened mandatory policy")
	}
}

func TestOutboxDoesNotStarveNewWorkBehindFailedExports(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 120; i++ {
		if err = enqueue(ctx, tx, "export-run", fmt.Sprintf("old-%03d", i), map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	if err = enqueue(ctx, tx, "start-run", "new-admission", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range pending {
		if o.Aggregate == "new-admission" {
			found = true
		} else if err = s.CompleteOutbox(ctx, o.ID, "disk full"); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("failed exports starved admission")
	}
	pending, err = s.Pending(ctx)
	if err != nil || pending[0].Aggregate != "new-admission" {
		t.Fatal("retry scheduling did not prioritize new work", err)
	}
}
