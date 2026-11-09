package factory

import (
	"context"
	"testing"
)

func queueDepthTestStore(t *testing.T) *intakeStore {
	t.Helper()
	store, err := openIntakeStore(t.TempDir())
	if err != nil {
		t.Fatalf("openIntakeStore: %v", err)
	}
	return store
}

// claimQueueDepthRecord records one accepted work item and returns its identity.
func claimQueueDepthRecord(t *testing.T, store *intakeStore, id, scope string) intakeRecord {
	t.Helper()
	record := intakeRecord{
		Connector:    "azdo",
		Repository:   "https://dev.azure.com/acme/payments/_git/payments",
		WorkItemID:   id,
		Revision:     "1",
		SnapshotHash: "sha256:" + id,
		ChangeID:     "workitem-change-" + id,
		RunID:        "wi-run-" + id,
		ActivationID: "activation-" + id,
		Scope:        scope,
	}
	claim, err := store.Claim(context.Background(), record)
	if err != nil || !claim.Accepted {
		t.Fatalf("claim work item %s = %#v, %v", id, claim, err)
	}
	return record
}

func recordQueueDepthOutcome(t *testing.T, store *intakeStore, record intakeRecord, status string) {
	t.Helper()
	err := store.RecordOutcome(context.Background(), WorkItemOutcomeInput{
		Connector: record.Connector, Repository: record.Repository,
		WorkItemID: record.WorkItemID, Revision: record.Revision,
		Status: status,
	})
	if err != nil {
		t.Fatalf("record work item %s outcome: %v", record.WorkItemID, err)
	}
}

// TestIntakeStoreQueueDepthPinsEmptyMap proves a store with no scoped records
// returns an empty map rather than nil, so the gauge writer always iterates a
// usable snapshot.
func TestIntakeStoreQueueDepthPinsEmptyMap(t *testing.T) {
	t.Parallel()

	store := queueDepthTestStore(t)
	depths, err := store.QueueDepth()
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depths == nil {
		t.Fatal("QueueDepth returned nil; an empty store must return an empty map")
	}
	if len(depths) != 0 {
		t.Fatalf("QueueDepth = %v, want an empty map", depths)
	}

	// A record written before the scope was persisted contributes no series.
	claimQueueDepthRecord(t, store, "legacy", "")
	depths, err = store.QueueDepth()
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depths == nil || len(depths) != 0 {
		t.Fatalf("QueueDepth = %v, want an empty non-nil map", depths)
	}
}

// TestIntakeStoreQueueDepthDrainsToZero proves a scope stays in the snapshot
// with an explicit zero once its last pending record reaches an outcome, so the
// gauge can return to zero.
func TestIntakeStoreQueueDepthDrainsToZero(t *testing.T) {
	t.Parallel()

	store := queueDepthTestStore(t)
	record := claimQueueDepthRecord(t, store, "42", "payments")

	depths, err := store.QueueDepth()
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depths["payments"] != 1 {
		t.Fatalf("QueueDepth = %v, want one pending payments record", depths)
	}

	recordQueueDepthOutcome(t, store, record, intakeRecordSucceeded)
	depths, err = store.QueueDepth()
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth, ok := depths["payments"]; !ok || depth != 0 {
		t.Fatalf("QueueDepth = %v, want payments present with a zero depth", depths)
	}
}

// TestIntakeStoreQueueDepthTreatsDuplicateAsTerminal proves a duplicate outcome
// does not count as pending work forever.
func TestIntakeStoreQueueDepthTreatsDuplicateAsTerminal(t *testing.T) {
	t.Parallel()

	store := queueDepthTestStore(t)
	record := claimQueueDepthRecord(t, store, "42", "payments")
	recordQueueDepthOutcome(t, store, record, intakeStatusDuplicate)

	depths, err := store.QueueDepth()
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth, ok := depths["payments"]; !ok || depth != 0 {
		t.Fatalf("QueueDepth = %v, want payments present with a zero depth", depths)
	}
}
