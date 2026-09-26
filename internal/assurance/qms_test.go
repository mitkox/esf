package assurance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCAPALifecycleUsesIndependentCriteriaEvidence(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "quality.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	content := newContent(t)
	failed := candidateFixture(t, s, content, "failed", "server.go", false)
	manifest := func(Run, Decision, string) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
	failed, err = s.CommitResult(ctx, failed.ID, content, manifest, "", true)
	if err != nil {
		t.Fatal(err)
	}
	creator := Actor{ID: "creator", Kind: "human"}
	planner := Actor{ID: "planner", Kind: "human"}
	approver := Actor{ID: "approver", Kind: "human"}
	verifier := Actor{ID: "verifier", Kind: "human"}
	closer := Actor{ID: "closer", Kind: "human"}
	c, err := s.OpenCAPA(ctx, "capa-1", failed.ID, failed.NonConformances[0].ID, creator)
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.AdvanceCAPA(ctx, c.ID, planner, CAPAAction{Operation: "plan", Action: "plan", Reason: "investigated", RootCause: "missing boundary test", AcceptanceCriteria: "boundary regression passes and corrective bytes reviewed", CorrectiveActions: "fix guard", PreventiveActions: "add regression"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdvanceCAPA(ctx, c.ID, planner, CAPAAction{Operation: "self", Action: "approve-plan", Reason: "self"}); err == nil {
		t.Fatal("plan author approved own plan")
	}
	c, err = s.AdvanceCAPA(ctx, c.ID, approver, CAPAAction{Operation: "approve", Action: "approve-plan", Reason: "plan reviewed"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.GetCAPA(ctx, c.ID)
	if err != nil || Hash(recovered) != Hash(c) {
		t.Fatal("CAPA restart changed state", err)
	}
	var selected []string
	for _, kind := range []string{"corrective", "preventive"} {
		r := candidateFixture(t, s, content, kind, "server.go", true)
		if _, err = s.OpenApproval(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		r, err = s.CommitResult(ctx, r.ID, content, manifest, "", true)
		if err != nil {
			t.Fatal(err)
		}
		selected = append(selected, r.Evidence[0].Digest)
		c, err = s.AdvanceCAPA(ctx, c.ID, creator, CAPAAction{Operation: kind, Action: "link-" + kind, Reason: "implements approved plan", RunID: r.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	action := CAPAAction{Operation: "verify", Action: "verify", Reason: "reviewed selected evidence against each acceptance criterion", CriteriaDigest: "wrong", EvidenceDigests: selected}
	if _, err = s.AdvanceCAPA(ctx, c.ID, verifier, action); err == nil {
		t.Fatal("unbound criteria accepted")
	}
	action.CriteriaDigest = c.CriteriaDigest
	c, err = s.AdvanceCAPA(ctx, c.ID, verifier, action)
	if err != nil || c.Status != "closure" {
		t.Fatal(err)
	}
	if _, err = s.AdvanceCAPA(ctx, c.ID, verifier, CAPAAction{Operation: "self-close", Action: "close", Reason: "self"}); err == nil {
		t.Fatal("verifier closed own verification")
	}
	c, err = s.AdvanceCAPA(ctx, c.ID, closer, CAPAAction{Operation: "close", Action: "close", Reason: "effectiveness independently confirmed"})
	if err != nil || c.Status != "closed" {
		t.Fatal(err)
	}
	after, _ := s.Get(ctx, failed.ID)
	if after.Decision.Result != "rejected" || Hash(after) != Hash(failed) {
		t.Fatal("CAPA rewrote historical failure")
	}
}
func TestImportedProviderFactsAreImmutable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p := LocalProvider{s}
	fact := json.RawMessage(`{"requirement":"audit every decision"}`)
	if err := s.Import(ctx, "requirement", "R-1", fact); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, "requirement", "R-1", fact); err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, "requirement", "R-1", json.RawMessage(`{}`)); err == nil {
		t.Fatal("overwrote imported fact")
	}
	got, err := p.GetRequirement(ctx, "R-1")
	if err != nil || string(got) != string(fact) {
		t.Fatal(err)
	}
	if err = p.PublishReleaseRecord(ctx, "release", fact); err == nil {
		t.Fatal("patch provider claimed release authority")
	}
}
