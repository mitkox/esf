package factory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const lockedStoreTestRepository = "https://dev.azure.com/acme/payments/_git/payments"

// lockedStoreCase is one durable store sharing the common locked JSON state
// implementation. Writers use a distinct index so a concurrent run must keep
// every update.
type lockedStoreCase struct {
	name   string
	file   string
	write  func(artifactRoot string, index int) error
	verify func(t *testing.T, artifactRoot string)
}

func lockedStoreCases(writers int) []lockedStoreCase {
	return []lockedStoreCase{
		{
			name: "publication",
			file: filepath.Join(publicationDirName, publicationStateFileName),
			write: func(artifactRoot string, index int) error {
				store, err := openPublicationStore(artifactRoot)
				if err != nil {
					return err
				}
				_, err = store.RecordPublication(samplePublication(
					fmt.Sprintf("run-%d", index), fmt.Sprintf("pr-%d", index), "tree-locked"))
				return err
			},
			verify: func(t *testing.T, artifactRoot string) {
				t.Helper()
				store, err := openPublicationStore(artifactRoot)
				if err != nil {
					t.Fatalf("openPublicationStore: %v", err)
				}
				for i := 0; i < writers; i++ {
					pullRequestID := fmt.Sprintf("pr-%d", i)
					if _, ok, err := store.LoadByPullRequest("azdo", lockedStoreTestRepository, pullRequestID); err != nil || !ok {
						t.Fatalf("pull request %s was lost: ok=%v err=%v", pullRequestID, ok, err)
					}
				}
			},
		},
		{
			name: "scope slots",
			file: filepath.Join(scopeSlotDirName, scopeSlotStateFileName),
			write: func(artifactRoot string, index int) error {
				store, err := openScopeSlotStore(artifactRoot)
				if err != nil {
					return err
				}
				_, err = store.Acquire(context.Background(), "payments",
					fmt.Sprintf("run-%d", index), writers, time.Now().UTC())
				return err
			},
			verify: func(t *testing.T, artifactRoot string) {
				t.Helper()
				store, err := openScopeSlotStore(artifactRoot)
				if err != nil {
					t.Fatalf("openScopeSlotStore: %v", err)
				}
				active, err := store.Active(context.Background(), "payments", time.Now().UTC())
				if err != nil {
					t.Fatalf("Active: %v", err)
				}
				if active != writers {
					t.Fatalf("active leases = %d, want %d", active, writers)
				}
			},
		},
		{
			name: "intake",
			file: filepath.Join(intakeDirName, intakeStateFileName),
			write: func(artifactRoot string, index int) error {
				store, err := openIntakeStore(artifactRoot)
				if err != nil {
					return err
				}
				_, err = store.Claim(context.Background(), lockedStoreIntakeRecord(fmt.Sprintf("item-%d", index)))
				return err
			},
			verify: func(t *testing.T, artifactRoot string) {
				t.Helper()
				store, err := openIntakeStore(artifactRoot)
				if err != nil {
					t.Fatalf("openIntakeStore: %v", err)
				}
				state, err := store.Load()
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				if len(state.Records) != writers || len(state.Heads) != writers {
					t.Fatalf("records=%d heads=%d, want %d each",
						len(state.Records), len(state.Heads), writers)
				}
				for i := 0; i < writers; i++ {
					rec := lockedStoreIntakeRecord(fmt.Sprintf("item-%d", i))
					stored, ok := state.Records[intakeRecordKey(rec)]
					if !ok || stored.Status != intakeRecordAccepted {
						t.Fatalf("intake record %s was lost: ok=%v status=%q", rec.WorkItemID, ok, stored.Status)
					}
				}
			},
		},
	}
}

func lockedStoreIntakeRecord(workItemID string) intakeRecord {
	return intakeRecord{
		Connector:    "azdo",
		Repository:   lockedStoreTestRepository,
		WorkItemID:   workItemID,
		Revision:     "1",
		SnapshotHash: "sha256:" + strings.Repeat("a", 64),
		ChangeID:     "change-" + workItemID,
		RunID:        "run-" + workItemID,
		ActivationID: "activation-" + workItemID,
	}
}

// TestLockedStoreStateFilesAreOwnerOnly is the golden file-mode check: every
// store that replaces its state leaves the durable file readable only by its
// owner.
func TestLockedStoreStateFilesAreOwnerOnly(t *testing.T) {
	t.Parallel()

	for _, tc := range lockedStoreCases(1) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			artifactRoot := t.TempDir()
			if err := tc.write(artifactRoot, 0); err != nil {
				t.Fatalf("write state: %v", err)
			}
			info, err := os.Stat(filepath.Join(artifactRoot, tc.file))
			if err != nil {
				t.Fatalf("stat state file: %v", err)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Fatalf("state file mode = %o, want 600", mode)
			}
		})
	}
}

// TestLockedStoreSerializesConcurrentWriters proves the shared lock keeps every
// concurrent update: a lost write would drop one of the distinct records.
func TestLockedStoreSerializesConcurrentWriters(t *testing.T) {
	t.Parallel()

	const writers = 8
	for _, tc := range lockedStoreCases(writers) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			artifactRoot := t.TempDir()
			errs := make([]error, writers)
			var wg sync.WaitGroup
			wg.Add(writers)
			for i := 0; i < writers; i++ {
				go func(i int) {
					defer wg.Done()
					errs[i] = tc.write(artifactRoot, i)
				}(i)
			}
			wg.Wait()
			for i, err := range errs {
				if err != nil {
					t.Fatalf("writer %d: %v", i, err)
				}
			}
			tc.verify(t, artifactRoot)
		})
	}
}
