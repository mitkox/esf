package factory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/forge"
)

func TestAppendIntakeRejectionWritesDurableAudit(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	rejection := forge.Rejection{
		Connector:  "azdo",
		WorkItemID: "42",
		Revision:   "6",
		Actor:      forge.Actor{ID: "ada", Descriptor: "aad.ada-lovelace"},
		Reason:     "actor lacks repository write permission",
	}
	if err := AppendIntakeRejection(dataDir, rejection); err != nil {
		t.Fatalf("AppendIntakeRejection: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dataDir, intakeDirName, intakeAuditFileName))
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	var entry AuditEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if entry.Action != AuditIntake || entry.Outcome != AuditOutcomeRejected {
		t.Fatalf("audit action/outcome = %s/%s", entry.Action, entry.Outcome)
	}
	if entry.Actor != "aad.ada-lovelace" || entry.ActorKind != ActorKindForge {
		t.Fatalf("audit actor = %q/%q", entry.Actor, entry.ActorKind)
	}
	if entry.At.IsZero() {
		t.Fatal("audit entry has no timestamp")
	}
	for key, want := range map[string]string{
		"connector":    "azdo",
		"work_item_id": "42",
		"revision":     "6",
		"status":       "rejected",
		"reason":       rejection.Reason,
	} {
		if got, _ := entry.Detail[key].(string); got != want {
			t.Fatalf("detail[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestAppendIntakeRejectionRequiresDataDir(t *testing.T) {
	t.Parallel()

	err := AppendIntakeRejection("", forge.Rejection{Connector: "azdo"})
	if err == nil || !strings.Contains(err.Error(), "artifact root is required") {
		t.Fatalf("AppendIntakeRejection() = %v, want a data-directory error", err)
	}
}
