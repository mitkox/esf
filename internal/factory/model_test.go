package factory

import (
	"encoding/json"
	"testing"
)

// TestWorkItemEvidenceReadsRecordsWithoutProject proves the project field is
// additive: manifests written before it existed still decode, and a new record
// carries the project status reporting needs.
func TestWorkItemEvidenceReadsRecordsWithoutProject(t *testing.T) {
	var legacy RunManifest
	if err := json.Unmarshal([]byte(`{"run_id":"run-1","work_item":{"connector":"azdo","id":"42","revision":"7"}}`), &legacy); err != nil {
		t.Fatalf("decode legacy manifest: %v", err)
	}
	if legacy.WorkItem == nil || legacy.WorkItem.ID != "42" || legacy.WorkItem.Project != "" {
		t.Fatalf("legacy work item = %#v", legacy.WorkItem)
	}
	encoded, err := json.Marshal(WorkItemEvidence{Connector: "azdo", ID: "42", Revision: "7", Project: "Payments"})
	if err != nil {
		t.Fatalf("marshal work item: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode work item: %v", err)
	}
	if decoded["project"] != "Payments" {
		t.Fatalf("encoded work item = %s, want a project field", encoded)
	}
}
