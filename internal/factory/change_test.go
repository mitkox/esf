package factory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/artifacts"
)

// mustJSON marshals a value or fails the test.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// These tests cover the durable Change (work item) — the AX-inspired parent of
// runs. The properties that matter:
//
//   - a change is idempotent by run ID, so a retried activity cannot inflate
//     attempts or spend;
//   - aggregates are recomputed from the durable manifests, never estimated;
//   - a terminal human decision is not overwritten by a later run;
//   - a change cannot be saved without an activation, because that would list
//     work that never ran.

// newChangeFixture returns a change store plus an artifact factory sharing one
// root, mirroring production layout.
func newChangeFixture(t *testing.T) (*ChangeStore, artifacts.Factory) {
	t.Helper()
	root := t.TempDir()
	factory, err := artifacts.NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	store, err := NewChangeStore(root)
	if err != nil {
		t.Fatalf("NewChangeStore: %v", err)
	}
	return store, factory
}

// writeManifest persists a minimal manifest so aggregates have a durable source.
func writeManifest(t *testing.T, factory artifacts.Factory, manifest RunManifest) {
	t.Helper()
	store, err := factory.ForRun(manifest.RunID)
	if err != nil {
		t.Fatalf("ForRun: %v", err)
	}
	if err := store.Write(ArtifactRun, mustJSON(t, manifest)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestChangeStoreSaveLoadRoundTrip(t *testing.T) {
	store, _ := newChangeFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	change := Change{
		ChangeID:          "change-1",
		Scope:             "payments",
		Repository:        "https://github.com/acme/payments",
		RequestedRevision: "abc",
		Task:              "fix the rounding bug",
		TaskHash:          "hash",
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            ChangeOpen,
		Activations:       []Activation{{RunID: "run-1", Reason: ActivationInitial, CreatedAt: now}},
		Attempts:          1,
	}
	if err := store.Save(change); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load("change-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ChangeID != change.ChangeID || loaded.Repository != change.Repository {
		t.Fatalf("loaded = %+v, want %+v", loaded, change)
	}
	if loaded.Status != ChangeOpen {
		t.Fatalf("status = %s, want OPEN", loaded.Status)
	}
}

func TestChangeStoreRejectsUnsafeIdentifiers(t *testing.T) {
	store, _ := newChangeFixture(t)
	for _, id := range []string{"", "../escape", "a/b", ".", ".."} {
		if _, err := store.Load(id); err == nil {
			t.Errorf("Load(%q) succeeded, want a rejection", id)
		}
	}
}

func TestChangeStoreRefusesChangeWithoutActivations(t *testing.T) {
	store, _ := newChangeFixture(t)
	if err := store.Save(Change{ChangeID: "empty", Status: ChangeOpen}); err == nil {
		t.Fatal("Save accepted a change with no activations")
	}
}

// TestRecordRunIsIdempotentByRunID proves a retried activity cannot inflate
// attempts or spend.
func TestRecordRunIsIdempotentByRunID(t *testing.T) {
	store, factory := newChangeFixture(t)
	cost := 1.25
	tokensIn := int64(100)
	tokensOut := int64(50)
	manifest := RunManifest{
		RunID:         "run-1",
		ChangeID:      "change-1",
		Repository:    "https://github.com/acme/payments",
		FactoryResult: StateSucceeded,
		StartedAt:     time.Now().UTC().Add(-time.Minute),
		CompletedAt:   time.Now().UTC(),
		InferenceCost: &cost,
		TokensIn:      &tokensIn,
		TokensOut:     &tokensOut,
	}
	writeManifest(t, factory, manifest)

	for i := 0; i < 3; i++ {
		change, err := store.RecordRun(manifest)
		if err != nil {
			t.Fatalf("RecordRun attempt %d: %v", i, err)
		}
		if change.Attempts != 1 {
			t.Fatalf("attempts = %d after %d records, want 1", change.Attempts, i+1)
		}
		if change.TotalCostUSD != cost {
			t.Fatalf("total cost = %v after %d records, want %v", change.TotalCostUSD, i+1, cost)
		}
		if change.TotalTokens != tokensIn+tokensOut {
			t.Fatalf("total tokens = %d after %d records, want %d", change.TotalTokens, i+1, tokensIn+tokensOut)
		}
	}
}

// TestRecordRunAggregatesAcrossActivations proves rework spend accumulates on
// the parent rather than being lost with the previous run.
func TestRecordRunAggregatesAcrossActivations(t *testing.T) {
	store, factory := newChangeFixture(t)
	cost1, cost2 := 1.0, 2.5
	tokens1, tokens2 := int64(10), int64(20)

	first := RunManifest{
		RunID: "run-1", ChangeID: "change-1", Repository: "https://github.com/acme/payments",
		FactoryResult: StateVerificationFailed, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
		InferenceCost: &cost1, TokensIn: &tokens1,
	}
	second := RunManifest{
		RunID: "run-2", ChangeID: "change-1", ParentRunID: "run-1", Repository: "https://github.com/acme/payments",
		FactoryResult: StateSucceeded, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
		InferenceCost: &cost2, TokensOut: &tokens2,
	}
	writeManifest(t, factory, first)
	writeManifest(t, factory, second)

	if _, err := store.RecordRun(first); err != nil {
		t.Fatalf("RecordRun first: %v", err)
	}
	change, err := store.RecordRun(second)
	if err != nil {
		t.Fatalf("RecordRun second: %v", err)
	}
	if change.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", change.Attempts)
	}
	if change.TotalCostUSD != cost1+cost2 {
		t.Fatalf("total cost = %v, want %v", change.TotalCostUSD, cost1+cost2)
	}
	if change.TotalTokens != tokens1+tokens2 {
		t.Fatalf("total tokens = %d, want %d", change.TotalTokens, tokens1+tokens2)
	}
	// The second activation's lineage is recorded.
	last := change.Activations[len(change.Activations)-1]
	if last.ParentRunID != "run-1" || last.Reason != ActivationRework {
		t.Fatalf("last activation = %+v, want a rework of run-1", last)
	}
	if change.Status != ChangeInReview {
		t.Fatalf("status = %s, want IN_REVIEW after a successful activation", change.Status)
	}
}

// TestChangeStatusFollowsHumanDecision proves approval closes a change and
// rejection returns it to OPEN without falsifying anything.
func TestChangeStatusFollowsHumanDecision(t *testing.T) {
	store, factory := newChangeFixture(t)
	manifest := RunManifest{
		RunID: "run-1", ChangeID: "change-1", Repository: "https://github.com/acme/x",
		FactoryResult: StateSucceeded, HumanResult: HumanResultRejected, ReviewNote: "please use the formal greeting",
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
	}
	writeManifest(t, factory, manifest)
	change, err := store.RecordRun(manifest)
	if err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if change.Status != ChangeOpen {
		t.Fatalf("status = %s, want OPEN after rejection", change.Status)
	}
	if change.ReworkNote != manifest.ReviewNote {
		t.Fatalf("rework_note = %q, want %q", change.ReworkNote, manifest.ReviewNote)
	}

	manifest.HumanResult = HumanResultApproved
	writeManifest(t, factory, manifest)
	change, err = store.RecordRun(manifest)
	if err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if change.Status != ChangeDone {
		t.Fatalf("status = %s, want DONE after approval", change.Status)
	}
}

// TestAppendActivationIsIdempotent proves rework submission cannot double-count.
func TestAppendActivationIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	change := Change{ChangeID: "change-1", Activations: []Activation{{RunID: "run-1"}}, Attempts: 1}
	for i := 0; i < 3; i++ {
		change = AppendActivation(change, "run-2", "run-1", ActivationRework, "task", "default", now)
	}
	if change.Attempts != 2 || len(change.Activations) != 2 {
		t.Fatalf("attempts = %d activations = %d, want 2", change.Attempts, len(change.Activations))
	}
}

// TestChangeWithoutManifestUsageContributesZero proves aggregates never invent
// a number when a harness reported nothing.
func TestChangeWithoutManifestUsageContributesZero(t *testing.T) {
	store, factory := newChangeFixture(t)
	manifest := RunManifest{
		RunID: "run-1", ChangeID: "change-1", Repository: "https://github.com/acme/x",
		FactoryResult: StateSucceeded, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
	}
	writeManifest(t, factory, manifest)
	change, err := store.RecordRun(manifest)
	if err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
	if change.TotalCostUSD != 0 || change.TotalTokens != 0 {
		t.Fatalf("aggregates = %v/%d, want zero when nothing was reported", change.TotalCostUSD, change.TotalTokens)
	}
}

func TestRecomputeTotalsDoesNotCreateMissingRunDirectory(t *testing.T) {
	store, factory := newChangeFixture(t)
	if _, err := store.recomputeTotals(Change{Activations: []Activation{{RunID: "missing-run"}}}); err != nil {
		t.Fatalf("recomputeTotals: %v", err)
	}
	path := filepath.Join(factory.Root(), "runs", "missing-run")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing manifest probe created %s", path)
	}
}
