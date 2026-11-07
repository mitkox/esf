package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// contractFixture is the checked-in wire shape of the current major version.
// The contract tests require every field here to remain present with the same
// JSON type; new fields are allowed within a major version.
type contractFixture struct {
	Request   json.RawMessage            `json:"request"`
	RunStatus map[string]json.RawMessage `json:"run_status"`
	Condition map[string]json.RawMessage `json:"condition"`
	Event     map[string]json.RawMessage `json:"event"`
	Evidence  map[string]json.RawMessage `json:"evidence"`
	Error     map[string]json.RawMessage `json:"error"`
}

func TestContractFixtureIsAdditiveOverCurrentWireShape(t *testing.T) {
	fixture := loadContractFixture(t)
	status := contractStatus()
	controller := newFakeController()
	controller.runs[status.RunID] = status
	controller.events = []Event{contractEvent()}
	baseURL := startServer(t, controller)

	t.Run("submit request", func(t *testing.T) {
		response, body := doRequest(t, http.MethodPost, baseURL+"/v1/runs", bytes.NewReader(fixture.Request))
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("submit status = %d, want %d: %s", response.StatusCode, http.StatusAccepted, body)
		}
		var request RunRequest
		if err := json.Unmarshal(fixture.Request, &request); err != nil {
			t.Fatalf("decode fixture request: %v", err)
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("encode run request: %v", err)
		}
		assertAdditive(t, "submit request", objectFields(t, fixture.Request), objectFields(t, encoded))
	})

	t.Run("run status", func(t *testing.T) {
		response, body := doRequest(t, http.MethodGet, baseURL+"/v1/runs/"+status.RunID, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
		}
		current := objectFields(t, body)
		assertAdditive(t, "run status", fixture.RunStatus, current)
		rawConditions, ok := current["conditions"]
		if !ok {
			t.Fatal("run status has no conditions to compare")
		}
		var conditions []json.RawMessage
		if err := json.Unmarshal(rawConditions, &conditions); err != nil {
			t.Fatalf("decode conditions: %v", err)
		}
		if len(conditions) != 1 {
			t.Fatalf("conditions = %d, want 1", len(conditions))
		}
		assertAdditive(t, "condition", fixture.Condition, objectFields(t, conditions[0]))
	})

	t.Run("events", func(t *testing.T) {
		response, body := doRequest(t, http.MethodGet, baseURL+"/v1/runs/"+status.RunID+"/events", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("events status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
		}
		var events []json.RawMessage
		if err := json.Unmarshal(body, &events); err != nil {
			t.Fatalf("decode events: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("events = %d, want 1", len(events))
		}
		assertAdditive(t, "event", fixture.Event, objectFields(t, events[0]))
	})

	t.Run("evidence", func(t *testing.T) {
		response, body := doRequest(t, http.MethodGet, baseURL+"/v1/runs/"+status.RunID+"/evidence", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("evidence status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
		}
		assertAdditive(t, "evidence", fixture.Evidence, objectFields(t, body))
	})

	t.Run("error response", func(t *testing.T) {
		response, body := doRequest(t, http.MethodGet, baseURL+"/v1/runs/run-missing", nil)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("error status = %d, want %d: %s", response.StatusCode, http.StatusNotFound, body)
		}
		assertAdditive(t, "error response", fixture.Error, objectFields(t, body))
	})
}

func loadContractFixture(t *testing.T) contractFixture {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "contract-v1.json"))
	if err != nil {
		t.Fatalf("read contract fixture: %v", err)
	}
	var fixture contractFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatalf("decode contract fixture: %v", err)
	}
	if len(fixture.RunStatus) == 0 || len(fixture.Request) == 0 {
		t.Fatal("contract fixture is empty")
	}
	return fixture
}

func contractStatus() RunStatus {
	started := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	return RunStatus{
		RunID:               "run-contract",
		WorkflowID:          "factory-run-run-contract",
		State:               "SUCCEEDED",
		CurrentStep:         "verify",
		Repository:          "https://github.com/acme/repository",
		Revision:            strings.Repeat("a", 40),
		Scope:               "team-a",
		ChangeID:            "change-contract",
		AgentHarness:        "opencode",
		VerificationProfile: "default",
		AgentResult:         "PASSED",
		VerificationResult:  "PASSED",
		StartedAt:           started,
		CompletedAt:         started.Add(5 * time.Minute),
		Conditions:          []Condition{contractCondition()},
		Error:               "none",
	}
}

func contractCondition() Condition {
	return Condition{
		Type:               "Validated",
		Status:             "True",
		Reason:             "ChecksPassed",
		Message:            "all checks passed",
		LastTransitionTime: time.Date(2026, 11, 7, 10, 2, 0, 0, time.UTC),
	}
}

func contractEvent() Event {
	return Event{
		Seq:     1,
		Type:    "Validated",
		Status:  "True",
		Reason:  "ChecksPassed",
		Message: "all checks passed",
		Time:    time.Date(2026, 11, 7, 10, 2, 0, 0, time.UTC),
	}
}

// objectFields decodes one JSON object into its fields, preserving each value's
// raw form so its JSON type can be compared.
func objectFields(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode JSON object %q: %v", raw, err)
	}
	return fields
}

// assertAdditive requires every fixture field to remain present with the same
// JSON type. Extra fields are permitted because the major version is
// additive-only; a missing or retyped field needs a new major version.
func assertAdditive(t *testing.T, label string, fixture, current map[string]json.RawMessage) {
	t.Helper()
	for name, want := range fixture {
		got, ok := current[name]
		if !ok {
			t.Errorf("%s no longer carries %q; removing or renaming a field requires a new major version", label, name)
			continue
		}
		if gotKind, wantKind := jsonKind(got), jsonKind(want); gotKind != wantKind {
			t.Errorf("%s field %q changed type from %s to %s; that requires a new major version", label, name, wantKind, gotKind)
		}
	}
}

func jsonKind(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "empty"
	}
	switch trimmed[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
