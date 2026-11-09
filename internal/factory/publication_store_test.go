package factory

import (
	"context"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

func samplePublication(runID, pullRequestID, tree string) publicationRecord {
	return publicationRecord{
		ChangeID: "change-1", RunID: runID, Connector: "azdo",
		Repository: "https://dev.azure.com/acme/payments/_git/payments",
		BaseBranch: "main", Branch: "esf/" + runID, PullRequestID: pullRequestID,
		PullRequestURL: "https://dev.azure.com/acme/payments/_git/payments/pullrequest/" + pullRequestID,
		Commit:         "c0ffee" + runID, Tree: tree,
	}
}

func acceptanceEvent(prID, tree, commit string, state forge.AcceptanceState, human bool) publicationAcceptanceEvent {
	return publicationAcceptanceEvent{
		Connector: "azdo", Repository: "https://dev.azure.com/acme/payments/_git/payments",
		ChangeID: "change-1", HumanClosed: human,
		Result: forge.AcceptanceResult{
			PullRequest: forge.PullRequest{ID: prID},
			State:       state, MergeCommit: commit, MergeTree: tree,
		},
	}
}

func TestPublicationStoreRepairsAcceptanceBeforePublication(t *testing.T) {
	t.Parallel()
	store, err := openPublicationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "tree-1", "merge-1", forge.AcceptanceMerged, true))
	if err != nil {
		t.Fatalf("RecordAcceptance: %v", err)
	}
	if outcome.Decision != publicationDecisionPending {
		t.Fatalf("decision = %q, want pending", outcome.Decision)
	}
	if _, err := store.RecordPublication(samplePublication("run-1", "17", "tree-1")); err != nil {
		t.Fatalf("RecordPublication: %v", err)
	}
	record, ok, err := store.Load("change-1")
	if err != nil || !ok {
		t.Fatalf("Load = %#v, %v", record, err)
	}
	if record.Pending || !record.Published {
		t.Fatalf("record = %#v, want a published record", record)
	}
	if record.Acceptance == nil || record.Acceptance.MergeCommit != "merge-1" {
		t.Fatalf("the early acceptance was lost: %#v", record.Acceptance)
	}

	// The observation is reapplied once the publication record exists.
	applied, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "tree-1", "merge-1", forge.AcceptanceMerged, true))
	if err != nil {
		t.Fatalf("RecordAcceptance after publication: %v", err)
	}
	if applied.Decision != publicationDecisionAccepted {
		t.Fatalf("decision = %q, want accepted", applied.Decision)
	}
	if applied.Record.LastMergeCommit != "merge-1" {
		t.Fatalf("last merge commit = %q", applied.Record.LastMergeCommit)
	}
}

func TestPublicationAcceptanceIsIdempotentPerMergeCommit(t *testing.T) {
	t.Parallel()
	store, err := openPublicationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPublication(samplePublication("run-1", "17", "tree-1")); err != nil {
		t.Fatal(err)
	}
	first, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "tree-1", "merge-1", forge.AcceptanceMerged, true))
	if err != nil || first.Decision != publicationDecisionAccepted {
		t.Fatalf("first acceptance = %#v, %v", first, err)
	}
	second, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "tree-1", "merge-1", forge.AcceptanceMerged, true))
	if err != nil || second.Decision != publicationDecisionAlreadyApplied {
		t.Fatalf("second acceptance = %#v, %v", second, err)
	}
}

func TestPublicationAcceptanceRejectsTreeDrift(t *testing.T) {
	t.Parallel()
	store, err := openPublicationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPublication(samplePublication("run-1", "17", "tree-1")); err != nil {
		t.Fatal(err)
	}
	outcome, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "tree-2", "merge-1", forge.AcceptanceMerged, true))
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Decision != publicationDecisionDrift {
		t.Fatalf("decision = %q, want drift", outcome.Decision)
	}
}

func TestPublicationAcceptanceIgnoresSupersededPullRequest(t *testing.T) {
	t.Parallel()
	store, err := openPublicationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPublication(samplePublication("run-1", "17", "tree-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPublication(samplePublication("run-2", "18", "tree-2")); err != nil {
		t.Fatal(err)
	}
	stale, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "tree-1", "merge-1", forge.AcceptanceMerged, true))
	if err != nil {
		t.Fatal(err)
	}
	if stale.Decision != publicationDecisionIgnoredStale {
		t.Fatalf("stale decision = %q, want ignored_stale", stale.Decision)
	}
	current, err := store.RecordAcceptance(context.Background(), acceptanceEvent("18", "tree-2", "merge-2", forge.AcceptanceMerged, true))
	if err != nil {
		t.Fatal(err)
	}
	if current.Decision != publicationDecisionAccepted {
		t.Fatalf("current decision = %q, want accepted", current.Decision)
	}
}

func TestPublicationAcceptanceDistinguishesServiceClose(t *testing.T) {
	t.Parallel()
	store, err := openPublicationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordPublication(samplePublication("run-1", "17", "tree-1")); err != nil {
		t.Fatal(err)
	}
	service, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "", "", forge.AcceptanceClosed, false))
	if err != nil {
		t.Fatal(err)
	}
	if service.Decision != publicationDecisionClosedService {
		t.Fatalf("service close = %q", service.Decision)
	}
	human, err := store.RecordAcceptance(context.Background(), acceptanceEvent("17", "", "", forge.AcceptanceClosed, true))
	if err != nil {
		t.Fatal(err)
	}
	if human.Decision != publicationDecisionClosedHuman {
		t.Fatalf("human close = %q", human.Decision)
	}
}

func TestPublicationStoreKeepsOneOpenPullRequestPerChange(t *testing.T) {
	t.Parallel()
	store, err := openPublicationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.RecordPublication(samplePublication("run-1", "17", "tree-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RecordPublication(samplePublication("run-2", "18", "tree-2"))
	if err != nil {
		t.Fatal(err)
	}
	if first.PullRequestID == second.PullRequestID {
		t.Fatal("the same pull request was recorded for two runs")
	}
	current, ok, err := store.Load("change-1")
	if err != nil || !ok || current.PullRequestID != "18" {
		t.Fatalf("current = %#v, %v", current, err)
	}
	old, ok, err := store.LoadByPullRequest("azdo", current.Repository, "17")
	if err != nil || !ok {
		t.Fatalf("old record unavailable: %v", err)
	}
	if !old.Superseded {
		t.Fatal("the previous pull request was not marked superseded")
	}
	_ = time.Now()
}
