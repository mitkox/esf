package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/api"
	"github.com/mitkox/esf/internal/factory"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/verification"
)

func apiRequest() api.RunRequest {
	return api.RunRequest{
		RunID:      "run-1",
		Repository: "https://example.com/acme/repo",
		Revision:   "main",
		Task:       "fix the bug",
	}
}

func TestAPISubmitClosedRunIsTypedTerminal(t *testing.T) {
	t.Parallel()

	controller := &apiController{
		config: factory.Default(),
		startWorkflow: func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
			return &serviceerror.WorkflowExecutionAlreadyStarted{Message: "already started"}
		},
	}
	_, err := controller.Submit(context.Background(), apiRequest())
	if !errors.Is(err, api.ErrRunTerminal) {
		t.Fatalf("Submit error = %v, want ErrRunTerminal", err)
	}
	if errors.Is(err, api.ErrRunConflict) {
		t.Fatalf("a closed run was reported as a conflict: %v", err)
	}
}

func TestAPISubmitReusedRunRejectsDifferentRequest(t *testing.T) {
	t.Parallel()

	recorded := factory.RunStatus{
		RunID:      "run-1",
		State:      factory.StateRunning,
		Repository: "https://example.com/acme/other",
		Revision:   "release",
		TaskHash:   repository.HashTask("the recorded task"),
	}
	controller := &apiController{
		config: factory.Default(),
		startWorkflow: func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
			return nil
		},
		queryStatus: func(context.Context, string) (factory.RunStatus, error) {
			return recorded, nil
		},
	}
	_, err := controller.Submit(context.Background(), apiRequest())
	if !errors.Is(err, api.ErrRunConflict) {
		t.Fatalf("Submit error = %v, want ErrRunConflict", err)
	}
}

func TestAPISubmitReusedRunReturnsRecordedStatus(t *testing.T) {
	t.Parallel()

	request := apiRequest()
	recorded := factory.RunStatus{
		RunID:      request.RunID,
		State:      factory.StateRunning,
		Repository: request.Repository,
		Revision:   request.Revision,
		TaskHash:   repository.HashTask(request.Task),
	}
	controller := &apiController{
		config: factory.Default(),
		startWorkflow: func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
			return nil
		},
		queryStatus: func(context.Context, string) (factory.RunStatus, error) {
			return recorded, nil
		},
	}
	got, err := controller.Submit(context.Background(), request)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got.State != string(factory.StateRunning) {
		t.Fatalf("state = %q, want the recorded RUNNING state", got.State)
	}
	if got.Repository != recorded.Repository || got.Revision != recorded.Revision {
		t.Fatalf("status = %#v, want the recorded request identity", got)
	}
}

// TestAPISubmitMapsSharedRequestFields anchors the API side of the submission
// contract to production code: it drives a valid Submit and inspects the
// factory.RunRequest the workflow start seam receives. A literal built in the
// test would not catch drift in the API's own mapping, and the explicit case
// proves a caller-chosen resource is forwarded instead of defaulted.
func TestAPISubmitMapsSharedRequestFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		harness     string
		profile     string
		wantHarness string
		wantProfile string
	}{
		{
			name:        "operator defaults",
			wantHarness: "api-default-harness",
			wantProfile: "api-default-profile",
		},
		{
			name:        "explicit resources",
			harness:     "api-explicit-harness",
			profile:     "api-explicit-profile",
			wantHarness: "api-explicit-harness",
			wantProfile: "api-explicit-profile",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := factory.Default()
			cfg.Cube.TemplateID = "tpl-api-test"
			cfg.Harnesses = map[string]factory.HarnessConfig{
				"api-default-harness":  {},
				"api-explicit-harness": {},
			}
			cfg.Verification = map[string]verification.Profile{
				"api-default-profile":  {},
				"api-explicit-profile": {},
			}

			controller, _ := newAPITestController(t)
			controller.config = cfg
			var started factory.RunRequest
			controller.startWorkflow = func(_ context.Context, _ client.StartWorkflowOptions, run factory.RunRequest) error {
				started = run
				return nil
			}

			request := apiRequest()
			request.Scope = "payments"
			request.ChangeID = "change-42"
			request.AgentModel = "agent-model"
			request.AgentHarness = tc.harness
			request.VerificationProfile = tc.profile
			request.Workspace = "warm-workspace"
			request.Model = "model-resource"
			request.EgressPolicy = "egress-strict"
			request.ParentRunID = "run-parent"
			if _, err := controller.Submit(context.Background(), request); err != nil {
				t.Fatalf("Submit: %v", err)
			}

			checks := []struct {
				name string
				got  any
				want any
			}{
				{name: "run id", got: started.RunID, want: request.RunID},
				{name: "repository", got: started.Repository, want: request.Repository},
				{name: "revision", got: started.Revision, want: request.Revision},
				{name: "task", got: started.Task, want: request.Task},
				{name: "repository kind", got: started.RepositoryKind, want: "remote"},
				{name: "harness", got: started.AgentHarness, want: tc.wantHarness},
				{name: "verification profile", got: started.VerificationProfile, want: tc.wantProfile},
				{name: "sandbox template", got: started.SandboxTemplate, want: "tpl-api-test"},
				{name: "agent timeout", got: started.AgentTimeout, want: cfg.Limits.AgentTimeout.Std()},
				{name: "verification timeout", got: started.VerificationTimeout, want: cfg.Limits.VerificationTimeout.Std()},
				{name: "total timeout", got: started.TotalTimeout, want: cfg.Limits.TotalTimeout.Std()},
				{name: "scope", got: started.Scope, want: request.Scope},
				{name: "change id", got: started.ChangeID, want: request.ChangeID},
				{name: "agent model", got: started.AgentModel, want: request.AgentModel},
				{name: "workspace", got: started.Workspace, want: request.Workspace},
				{name: "model", got: started.Model, want: request.Model},
				{name: "egress policy", got: started.EgressPolicy, want: request.EgressPolicy},
				{name: "parent run id", got: started.ParentRunID, want: request.ParentRunID},
			}
			for _, check := range checks {
				if check.got != check.want {
					t.Errorf("%s = %v, want %v", check.name, check.got, check.want)
				}
			}
		})
	}
}

const apiTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" //gitleaks:allow

// newAPITestController returns the production controller over a temporary
// artifact store. Test seams stand in for Temporal; a manifest written to the
// store takes precedence over the query fallback.
func newAPITestController(t *testing.T) (*apiController, *artifacts.Local) {
	t.Helper()
	local, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return &apiController{
		config:    factory.Default(),
		artifacts: local,
		startWorkflow: func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
			return nil
		},
		queryStatus: func(context.Context, string) (factory.RunStatus, error) {
			return factory.RunStatus{}, &serviceerror.NotFound{Message: "workflow not found"}
		},
		cancelRun: func(context.Context, string) error {
			return &serviceerror.NotFound{Message: "workflow not found"}
		},
	}, local
}

func writeRunManifest(t *testing.T, local *artifacts.Local, runID string, body []byte) {
	t.Helper()
	store, err := local.ForRun(runID)
	if err != nil {
		t.Fatalf("ForRun(%q): %v", runID, err)
	}
	if err := store.Write(factory.ArtifactRun, body); err != nil {
		t.Fatalf("write run manifest: %v", err)
	}
}

// startAPITestServer serves the production controller over loopback until the
// test finishes.
func startAPITestServer(t *testing.T, controller *apiController) string {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "api.token")
	if err := os.WriteFile(tokenFile, []byte(apiTestToken+"\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	server, err := api.New(api.Config{
		Listen:     "127.0.0.1:0",
		TokenFile:  tokenFile,
		Controller: controller,
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, func(address net.Addr) { ready <- address }) }()
	var address net.Addr
	select {
	case address = <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("Serve exited before listening: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("server did not start")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return "http://" + address.String()
}

func doAPIRequest(t *testing.T, method, url string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiTestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return response, data
}

func TestAPISubmitRejectsRunIDTheStoreCannotAddress(t *testing.T) {
	t.Parallel()

	controller, _ := newAPITestController(t)
	var starts atomic.Int64
	controller.startWorkflow = func(context.Context, client.StartWorkflowOptions, factory.RunRequest) error {
		starts.Add(1)
		return nil
	}
	baseURL := startAPITestServer(t, controller)

	cases := []struct {
		name        string
		runID       string
		wantMessage string
	}{
		{name: "empty", runID: ""},
		{name: "dot", runID: "."},
		{name: "dotdot", runID: ".."},
		{name: "slash", runID: "a/b"},
		{name: "backslash", runID: `a\b`},
		{name: "nul byte", runID: "run\x00id", wantMessage: "NUL byte"},
		{name: "longer than 255 bytes", runID: strings.Repeat("a", 256), wantMessage: "255-byte filesystem name limit"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			request := apiRequest()
			request.RunID = tc.runID
			payload, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("encode request: %v", err)
			}
			response, body := doAPIRequest(t, http.MethodPost, baseURL+"/v1/runs", bytes.NewReader(payload))
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusBadRequest, body)
			}
			var failure struct {
				Code  string `json:"code"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(body, &failure); err != nil {
				t.Fatalf("decode error response %q: %v", body, err)
			}
			if failure.Code != "invalid_request" {
				t.Fatalf("error code = %q, want invalid_request", failure.Code)
			}
			if tc.wantMessage != "" && !strings.Contains(failure.Error, tc.wantMessage) {
				t.Fatalf("error message = %q, want it to name %q", failure.Error, tc.wantMessage)
			}
		})
	}
	if got := starts.Load(); got != 0 {
		t.Fatalf("workflow starts = %d, want 0 for rejected run IDs", got)
	}
}

const apiRunManifest = `{
  "run_id": "run-manifest",
  "workflow_id": "factory-run-run-manifest",
  "factory_result": "RUNNING",
  "repository": "https://example.com/acme/repo",
  "requested_revision": "main",
  "agent_harness": "opencode",
  "verification_profile": "default",
  "change_id": "change-1",
  "conditions": [
    {
      "type": "Validated",
      "status": "Unknown",
      "message": "checks <pending> & queued",
      "last_transition_time": "2026-11-08T10:00:00Z"
    },
    {
      "type": "AgentCompleted",
      "status": "Unknown",
      "last_transition_time": "2026-11-08T10:01:00Z"
    }
  ]
}`

func TestAPIStatusAndEvidenceReadTheStoredManifest(t *testing.T) {
	t.Parallel()

	controller, local := newAPITestController(t)
	writeRunManifest(t, local, "run-manifest", []byte(apiRunManifest))
	baseURL := startAPITestServer(t, controller)

	response, body := doAPIRequest(t, http.MethodGet, baseURL+"/v1/runs/run-manifest", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
	}
	var status api.RunStatus
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("decode status %q: %v", body, err)
	}
	if status.RunID != "run-manifest" || status.WorkflowID != "factory-run-run-manifest" {
		t.Fatalf("status identity = %#v", status)
	}
	if status.State != "RUNNING" || status.Repository != "https://example.com/acme/repo" || status.Revision != "main" {
		t.Fatalf("status = %#v, want the manifest's state and request identity", status)
	}
	if status.ChangeID != "change-1" || status.AgentHarness != "opencode" || status.VerificationProfile != "default" {
		t.Fatalf("status resources/lineage = %#v", status)
	}
	if len(status.Conditions) != 2 || status.Conditions[0].Type != "Validated" || status.Conditions[0].Status != "Unknown" {
		t.Fatalf("conditions = %#v", status.Conditions)
	}

	response, body = doAPIRequest(t, http.MethodGet, baseURL+"/v1/runs/run-manifest/evidence", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("evidence status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
	}
	var evidence api.Evidence
	if err := json.Unmarshal(body, &evidence); err != nil {
		t.Fatalf("decode evidence %q: %v", body, err)
	}
	if evidence.RunID != "run-manifest" {
		t.Fatalf("evidence run_id = %q", evidence.RunID)
	}
	if bytes.Equal(evidence.Manifest, []byte(apiRunManifest)) {
		t.Fatal("manifest was returned byte for byte; the contract promises a semantically equal JSON re-encoding")
	}
	if bytes.Contains(evidence.Manifest, []byte("<pending>")) {
		t.Fatalf("manifest was not HTML-escaped: %s", evidence.Manifest)
	}
	var got, want any
	if err := json.Unmarshal(evidence.Manifest, &got); err != nil {
		t.Fatalf("decode returned manifest %q: %v", evidence.Manifest, err)
	}
	if err := json.Unmarshal([]byte(apiRunManifest), &want); err != nil {
		t.Fatalf("decode stored manifest: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest is not semantically equal to the stored run.json:\ngot:  %s\nwant: %s", evidence.Manifest, apiRunManifest)
	}
}

const apiEventManifestUnknown = `{
  "run_id": "run-events",
  "workflow_id": "factory-run-run-events",
  "factory_result": "RUNNING",
  "repository": "https://example.com/acme/repo",
  "requested_revision": "main",
  "agent_harness": "opencode",
  "verification_profile": "default",
  "conditions": [
    {
      "type": "Validated",
      "status": "Unknown",
      "last_transition_time": "2026-11-08T10:00:00Z"
    },
    {
      "type": "AgentCompleted",
      "status": "Unknown",
      "last_transition_time": "2026-11-08T10:01:00Z"
    }
  ]
}`

const apiEventManifestValidated = `{
  "run_id": "run-events",
  "workflow_id": "factory-run-run-events",
  "factory_result": "RUNNING",
  "repository": "https://example.com/acme/repo",
  "requested_revision": "main",
  "agent_harness": "opencode",
  "verification_profile": "default",
  "conditions": [
    {
      "type": "Validated",
      "status": "True",
      "reason": "ChecksPassed",
      "last_transition_time": "2026-11-08T10:05:00Z"
    },
    {
      "type": "AgentCompleted",
      "status": "Unknown",
      "last_transition_time": "2026-11-08T10:01:00Z"
    }
  ]
}`

// TestAPIEventsReportCurrentConditionSnapshots pins the documented events
// contract: seq identifies a condition, not a transition, and the cursor does
// not imply that an in-place transition is delivered.
func TestAPIEventsReportCurrentConditionSnapshots(t *testing.T) {
	t.Parallel()

	controller, local := newAPITestController(t)
	writeRunManifest(t, local, "run-events", []byte(apiEventManifestUnknown))
	baseURL := startAPITestServer(t, controller)

	response, body := doAPIRequest(t, http.MethodGet, baseURL+"/v1/runs/run-events/events", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
	}
	var events []api.Event
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("decode events %q: %v", body, err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[0].Type != "Validated" || events[0].Status != "Unknown" {
		t.Fatalf("initial events = %#v", events)
	}
	firstTransition := events[0].Time

	writeRunManifest(t, local, "run-events", []byte(apiEventManifestValidated))

	response, body = doAPIRequest(t, http.MethodGet, baseURL+"/v1/runs/run-events/events?after=2", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events after status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
	}
	var afterCursor []api.Event
	if err := json.Unmarshal(body, &afterCursor); err != nil {
		t.Fatalf("decode events after cursor %q: %v", body, err)
	}
	if len(afterCursor) != 0 {
		t.Fatalf("after cursor returned %#v; a same-index transition is not a new event", afterCursor)
	}

	response, body = doAPIRequest(t, http.MethodGet, baseURL+"/v1/runs/run-events/events", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events reread status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
	}
	if err := json.Unmarshal(body, &events); err != nil {
		t.Fatalf("decode reread events %q: %v", body, err)
	}
	if len(events) != 2 {
		t.Fatalf("reread events = %#v", events)
	}
	if events[0].Seq != 1 {
		t.Fatalf("seq changed from 1 to %d after an in-place transition", events[0].Seq)
	}
	if events[0].Status != "True" || events[0].Reason != "ChecksPassed" {
		t.Fatalf("event did not carry the new condition state: %#v", events[0])
	}
	if !events[0].Time.After(firstTransition) {
		t.Fatalf("event time = %v, want a time after the first snapshot %v", events[0].Time, firstTransition)
	}
}

func TestAPIStatusFallsBackToTemporalQuery(t *testing.T) {
	t.Parallel()

	controller, _ := newAPITestController(t)
	started := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	wanted := factory.RunStatus{
		RunID:       "run-temporal",
		State:       factory.StateRunning,
		CurrentStep: "agent",
		Repository:  "https://example.com/acme/repo",
		Revision:    "main",
		StartedAt:   started,
		Conditions: []factory.Condition{{
			Type:               factory.ConditionValidated,
			Status:             factory.ConditionTrue,
			LastTransitionTime: started.Add(time.Minute),
		}},
	}
	queried := ""
	controller.queryStatus = func(_ context.Context, runID string) (factory.RunStatus, error) {
		queried = runID
		return wanted, nil
	}

	status, err := controller.Status(context.Background(), "run-temporal")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if queried != "run-temporal" {
		t.Fatalf("queried run = %q, want run-temporal", queried)
	}
	if status.RunID != "run-temporal" || status.WorkflowID != factory.WorkflowIDForRun("run-temporal") {
		t.Fatalf("status identity = %#v", status)
	}
	if status.State != string(factory.StateRunning) || status.CurrentStep != "agent" {
		t.Fatalf("status = %#v", status)
	}
	if status.Repository != wanted.Repository || status.Revision != wanted.Revision {
		t.Fatalf("status identity fields = %#v", status)
	}
	if len(status.Conditions) != 1 || status.Conditions[0].Type != "Validated" || status.Conditions[0].Status != "True" {
		t.Fatalf("conditions = %#v", status.Conditions)
	}

	controller.queryStatus = func(context.Context, string) (factory.RunStatus, error) {
		return factory.RunStatus{}, &serviceerror.NotFound{Message: "workflow history is gone"}
	}
	if _, err := controller.Status(context.Background(), "run-gone"); !errors.Is(err, api.ErrRunNotFound) {
		t.Fatalf("Status error = %v, want ErrRunNotFound", err)
	}
}

func TestAPICancelMapsNotFoundAndReturnsCurrentStatus(t *testing.T) {
	t.Parallel()

	controller, _ := newAPITestController(t)
	controller.cancelRun = func(context.Context, string) error {
		return fmt.Errorf("cancel run run-missing: %w", &serviceerror.NotFound{Message: "workflow not found"})
	}
	if _, err := controller.Cancel(context.Background(), "run-missing"); !errors.Is(err, api.ErrRunNotFound) {
		t.Fatalf("Cancel error = %v, want ErrRunNotFound", err)
	}

	wanted := factory.RunStatus{RunID: "run-cancel", State: factory.StateCancelled, CurrentStep: "cleanup"}
	controller.cancelRun = func(context.Context, string) error { return nil }
	controller.queryStatus = func(context.Context, string) (factory.RunStatus, error) { return wanted, nil }
	status, err := controller.Cancel(context.Background(), "run-cancel")
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if status.RunID != "run-cancel" || status.State != string(factory.StateCancelled) {
		t.Fatalf("cancelled status = %#v", status)
	}
}

func TestAPIMissingRunIsNotFoundOnEveryReadPath(t *testing.T) {
	t.Parallel()

	controller, _ := newAPITestController(t)
	baseURL := startAPITestServer(t, controller)
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{name: "status", method: http.MethodGet, path: "/v1/runs/run-missing"},
		{name: "events", method: http.MethodGet, path: "/v1/runs/run-missing/events"},
		{name: "evidence", method: http.MethodGet, path: "/v1/runs/run-missing/evidence"},
		{name: "cancel", method: http.MethodPost, path: "/v1/runs/run-missing/cancel"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			response, body := doAPIRequest(t, tc.method, baseURL+tc.path, nil)
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusNotFound, body)
			}
			var failure struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(body, &failure); err != nil {
				t.Fatalf("decode error response %q: %v", body, err)
			}
			if failure.Code != "run_not_found" {
				t.Fatalf("error code = %q, want run_not_found", failure.Code)
			}
		})
	}
}
