package apiclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/apiclient"
)

const testToken = "0123456789abcdef0123456789abcdef"

func testTime() time.Time {
	return time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
}

func fullStatus() apiclient.RunStatus {
	started := testTime()
	return apiclient.RunStatus{
		RunID:               "run-1",
		WorkflowID:          "factory-run-run-1",
		State:               "SUCCEEDED",
		CurrentStep:         "verify",
		Repository:          "https://github.com/acme/repository",
		Revision:            strings.Repeat("a", 40),
		Scope:               "team-a",
		ChangeID:            "change-1",
		AgentHarness:        "opencode",
		VerificationProfile: "default",
		AgentResult:         "PASSED",
		VerificationResult:  "PASSED",
		StartedAt:           started,
		CompletedAt:         started.Add(5 * time.Minute),
		Conditions: []apiclient.Condition{{
			Type:               "Validated",
			Status:             "True",
			Reason:             "ChecksPassed",
			Message:            "all checks passed",
			LastTransitionTime: started.Add(2 * time.Minute),
		}},
		Error: "none",
	}
}

func fullRequest() apiclient.RunRequest {
	return apiclient.RunRequest{
		RunID:               "run-1",
		Repository:          "https://github.com/acme/repository",
		Revision:            strings.Repeat("a", 40),
		Task:                "fix the failing test",
		AgentHarness:        "opencode",
		AgentModel:          "gpt-5",
		VerificationProfile: "default",
		Scope:               "team-a",
		Workspace:           "workspace-a",
		Model:               "gpt-5.1",
		EgressPolicy:        "default",
		ChangeID:            "change-1",
		ParentRunID:         "run-0",
	}
}

func newTestClient(t *testing.T, handler http.Handler) *apiclient.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := apiclient.New(server.URL, testToken)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func writeTestJSON(t *testing.T, response http.ResponseWriter, status int, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}

func TestSubmitStatusEventsEvidenceAndCancel(t *testing.T) {
	status := fullStatus()
	request := fullRequest()
	event := apiclient.Event{
		Seq:     1,
		Type:    "Validated",
		Status:  "True",
		Reason:  "ChecksPassed",
		Message: "all checks passed",
		Time:    status.StartedAt.Add(2 * time.Minute),
	}
	evidence := apiclient.Evidence{
		RunID:    status.RunID,
		Manifest: json.RawMessage(`{"run_id":"run-1"}`),
	}
	submissions := make(chan apiclient.RunRequest, 1)
	cursors := make(chan string, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", func(response http.ResponseWriter, request *http.Request) {
		var submission apiclient.RunRequest
		if err := json.NewDecoder(request.Body).Decode(&submission); err != nil {
			t.Errorf("decode submission: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		submissions <- submission
		writeTestJSON(t, response, http.StatusAccepted, status)
	})
	mux.HandleFunc("GET /v1/runs/run-1", func(response http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, response, http.StatusOK, status)
	})
	mux.HandleFunc("GET /v1/runs/run-1/events", func(response http.ResponseWriter, request *http.Request) {
		cursors <- request.URL.Query().Get("after")
		writeTestJSON(t, response, http.StatusOK, []apiclient.Event{event})
	})
	mux.HandleFunc("GET /v1/runs/run-1/evidence", func(response http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, response, http.StatusOK, evidence)
	})
	mux.HandleFunc("POST /v1/runs/run-1/cancel", func(response http.ResponseWriter, _ *http.Request) {
		cancelled := status
		cancelled.State = "CANCELLED"
		writeTestJSON(t, response, http.StatusAccepted, cancelled)
	})
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+testToken {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(response, request)
	}))

	ctx := context.Background()
	submitted, err := client.Submit(ctx, request)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case got := <-submissions:
		if !reflect.DeepEqual(got, request) {
			t.Fatalf("submitted request = %#v, want %#v", got, request)
		}
	default:
		t.Fatal("server did not receive the submission")
	}
	if !reflect.DeepEqual(submitted, status) {
		t.Fatalf("submitted status = %#v, want %#v", submitted, status)
	}

	current, err := client.Status(ctx, status.RunID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !reflect.DeepEqual(current, status) {
		t.Fatalf("status = %#v, want %#v", current, status)
	}

	events, err := client.Events(ctx, status.RunID, 2)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	select {
	case cursor := <-cursors:
		if cursor != "2" {
			t.Fatalf("event cursor = %q, want 2", cursor)
		}
	default:
		t.Fatal("server did not receive the event cursor")
	}
	if len(events) != 1 || !reflect.DeepEqual(events[0], event) {
		t.Fatalf("events = %#v, want %#v", events, []apiclient.Event{event})
	}

	gotEvidence, err := client.Evidence(ctx, status.RunID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	if !reflect.DeepEqual(gotEvidence, evidence) {
		t.Fatalf("evidence = %#v, want %#v", gotEvidence, evidence)
	}

	cancelled, err := client.Cancel(ctx, status.RunID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.State != "CANCELLED" || cancelled.RunID != status.RunID {
		t.Fatalf("cancelled status = %#v", cancelled)
	}
}

func TestStatusAcceptsUnknownAddedField(t *testing.T) {
	body := `{
		"run_id": "run-1",
		"workflow_id": "factory-run-run-1",
		"state": "SUCCEEDED",
		"started_at": "2026-11-07T10:00:00Z",
		"completed_at": "2026-11-07T10:05:00Z",
		"conditions": [{
			"type": "Validated",
			"status": "True",
			"last_transition_time": "2026-11-07T10:02:00Z",
			"future_condition_field": true
		}],
		"future_field": {"nested": true}
	}`
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		if _, err := response.Write([]byte(body)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))

	status, err := client.Status(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.RunID != "run-1" || status.State != "SUCCEEDED" {
		t.Fatalf("status = %#v", status)
	}
	if len(status.Conditions) != 1 || status.Conditions[0].Type != "Validated" {
		t.Fatalf("conditions = %#v", status.Conditions)
	}
}

func TestStatusMissingRequiredFieldReturnsResponseError(t *testing.T) {
	body := `{"workflow_id": "factory-run-run-1", "state": "SUCCEEDED"}`
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		if _, err := response.Write([]byte(body)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))

	_, err := client.Status(context.Background(), "run-1")
	var responseError *apiclient.ResponseError
	if !errors.As(err, &responseError) {
		t.Fatalf("Status error = %v, want *apiclient.ResponseError", err)
	}
	if responseError.Field != "run_id" {
		t.Fatalf("missing field = %q, want run_id", responseError.Field)
	}
}

func TestCancelUnknownRunSurfacesNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusNotFound)
		if _, err := response.Write([]byte(`{"error":"The run does not exist.","code":"run_not_found"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))

	_, err := client.Cancel(context.Background(), "run-missing")
	if !errors.Is(err, apiclient.ErrRunNotFound) {
		t.Fatalf("Cancel error = %v, want ErrRunNotFound", err)
	}
	var apiError *apiclient.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("Cancel error = %v, want *apiclient.APIError", err)
	}
	if apiError.StatusCode != http.StatusNotFound || apiError.Code != "run_not_found" {
		t.Fatalf("API error = %#v", apiError)
	}
}

func TestNewRejectsInvalidEndpointOrToken(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		token   string
	}{
		{name: "missing scheme", baseURL: "factory.example.com", token: testToken},
		{name: "missing host", baseURL: "https://", token: testToken},
		{name: "missing token", baseURL: "https://factory.example.com", token: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := apiclient.New(testCase.baseURL, testCase.token); err == nil {
				t.Fatal("New accepted an invalid configuration")
			}
		})
	}
}
