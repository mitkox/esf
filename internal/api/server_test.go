package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/factory"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// writeTokenFile writes a bearer token with the requested permissions.
func writeTokenFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api.token")
	if err := os.WriteFile(path, []byte(testToken+"\n"), mode); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	return path
}

// startServer serves the API over a loopback address until the test finishes.
func startServer(t *testing.T, controller Controller) string {
	t.Helper()
	server, err := New(Config{
		Listen:     "127.0.0.1:0",
		TokenFile:  writeTokenFile(t, 0o600),
		Controller: controller,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
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

func doRequest(t *testing.T, method, url string, body io.Reader) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+testToken)
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

type fakeController struct {
	mu     sync.Mutex
	runs   map[string]RunStatus
	starts int
	events []Event
}

func newFakeController() *fakeController {
	return &fakeController{runs: map[string]RunStatus{}}
}

func (f *fakeController) Submit(_ context.Context, request RunRequest) (RunStatus, error) {
	if request.RunID == "" || request.Task == "" || request.Repository == "" || request.Revision == "" {
		return RunStatus{}, ErrInvalidRequest
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if status, ok := f.runs[request.RunID]; ok {
		return status, nil
	}
	f.starts++
	status := RunStatus{
		RunID:               request.RunID,
		WorkflowID:          factory.WorkflowIDForRun(request.RunID),
		State:               string(factory.StateRequested),
		Repository:          request.Repository,
		Revision:            request.Revision,
		AgentHarness:        request.AgentHarness,
		VerificationProfile: request.VerificationProfile,
		StartedAt:           time.Now().UTC(),
	}
	f.runs[request.RunID] = status
	return status, nil
}

func (f *fakeController) Status(_ context.Context, runID string) (RunStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.runs[runID]
	if !ok {
		return RunStatus{}, ErrRunNotFound
	}
	return status, nil
}

func (f *fakeController) Events(_ context.Context, runID string, after int) ([]Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.runs[runID]; !ok {
		return nil, ErrRunNotFound
	}
	var events []Event
	for _, event := range f.events {
		if event.Seq > after {
			events = append(events, event)
		}
	}
	return events, nil
}

func (f *fakeController) Evidence(_ context.Context, runID string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.runs[runID]; !ok {
		return nil, ErrRunNotFound
	}
	return []byte(fmt.Sprintf(`{"run_id":%q}`, runID)), nil
}

func (f *fakeController) Cancel(_ context.Context, runID string) (RunStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.runs[runID]
	if !ok {
		return RunStatus{}, ErrRunNotFound
	}
	status.State = string(factory.StateCancelled)
	f.runs[runID] = status
	return status, nil
}

func decodeError(t *testing.T, body []byte) errorResponse {
	t.Helper()
	var response errorResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode error response %q: %v", body, err)
	}
	return response
}

func TestNewRefusesGroupReadableTokenFile(t *testing.T) {
	_, err := New(Config{
		Listen:     "127.0.0.1:0",
		TokenFile:  writeTokenFile(t, 0o640),
		Controller: newFakeController(),
	})
	if err == nil || !strings.Contains(err.Error(), "readable outside its owner") {
		t.Fatalf("group-readable token file must fail closed, got %v", err)
	}
}

func TestNewRefusesNonLoopbackWithoutTLS(t *testing.T) {
	_, err := New(Config{
		Listen:     "0.0.0.0:8090",
		TokenFile:  writeTokenFile(t, 0o600),
		Controller: newFakeController(),
	})
	if err == nil || !strings.Contains(err.Error(), "requires TLS") {
		t.Fatalf("non-loopback plaintext listener must be refused, got %v", err)
	}
}

func TestServerRejectsTwoAuthorizationHeaders(t *testing.T) {
	baseURL := startServer(t, newFakeController())
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/runs", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Add("Authorization", "Bearer "+testToken)
	request.Header.Add("Authorization", "Bearer "+testToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusUnauthorized, body)
	}
	if got := decodeError(t, body); got.Code != codeUnauthorized {
		t.Fatalf("error code = %q, want %q", got.Code, codeUnauthorized)
	}
}

func TestSubmissionIsIdempotentForTheSameRunIdentity(t *testing.T) {
	controller := newFakeController()
	baseURL := startServer(t, controller)
	payload := fmt.Sprintf(`{
		"run_id": "run-1",
		"repository": "https://github.com/acme/repository",
		"revision": %q,
		"task": "fix the failing test",
		"agent_harness": "opencode",
		"verification_profile": "default"
	}`, strings.Repeat("a", 40))

	firstResponse, firstBody := doRequest(t, http.MethodPost, baseURL+"/v1/runs", strings.NewReader(payload))
	secondResponse, secondBody := doRequest(t, http.MethodPost, baseURL+"/v1/runs", strings.NewReader(payload))
	if firstResponse.StatusCode != http.StatusAccepted || secondResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d, want %d", firstResponse.StatusCode, secondResponse.StatusCode, http.StatusAccepted)
	}
	if string(firstBody) != string(secondBody) {
		t.Fatalf("duplicate submission changed the response:\nfirst:  %s\nsecond: %s", firstBody, secondBody)
	}
	var status RunStatus
	if err := json.Unmarshal(firstBody, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.RunID != "run-1" || status.WorkflowID != factory.WorkflowIDForRun("run-1") {
		t.Fatalf("status identity = %#v", status)
	}
	controller.mu.Lock()
	starts := controller.starts
	controller.mu.Unlock()
	if starts != 1 {
		t.Fatalf("workflow starts = %d, want 1", starts)
	}
}

func TestServerRejectsOversizedBody(t *testing.T) {
	baseURL := startServer(t, newFakeController())
	payload := `{"task":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`
	response, body := doRequest(t, http.MethodPost, baseURL+"/v1/runs", strings.NewReader(payload))
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if got := decodeError(t, body); got.Code != codeRequestTooLarge {
		t.Fatalf("error code = %q, want %q", got.Code, codeRequestTooLarge)
	}
}

func TestStatusOfUnknownRunIsTypedNotFound(t *testing.T) {
	baseURL := startServer(t, newFakeController())
	response, body := doRequest(t, http.MethodGet, baseURL+"/v1/runs/run-missing", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, http.StatusNotFound, body)
	}
	if got := decodeError(t, body); got.Code != codeRunNotFound {
		t.Fatalf("error code = %q, want %q", got.Code, codeRunNotFound)
	}
}

func TestInspectEvidenceAndCancelEndpoints(t *testing.T) {
	controller := newFakeController()
	controller.events = []Event{{Seq: 1, Type: "Validated", Status: "True", Time: time.Now().UTC()}}
	baseURL := startServer(t, controller)
	payload := fmt.Sprintf(`{
		"run_id": "run-2",
		"repository": "https://github.com/acme/repository",
		"revision": %q,
		"task": "fix the failing test"
	}`, strings.Repeat("b", 40))
	if response, body := doRequest(t, http.MethodPost, baseURL+"/v1/runs", strings.NewReader(payload)); response.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d: %s", response.StatusCode, body)
	}

	response, body := doRequest(t, http.MethodGet, baseURL+"/v1/runs/run-2", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, body)
	}
	var status RunStatus
	if err := json.Unmarshal(body, &status); err != nil || status.RunID != "run-2" {
		t.Fatalf("status = %#v, err = %v", status, err)
	}

	response, body = doRequest(t, http.MethodGet, baseURL+"/v1/runs/run-2/events", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d: %s", response.StatusCode, body)
	}
	var events []Event
	if err := json.Unmarshal(body, &events); err != nil || len(events) != 1 || events[0].Type != "Validated" {
		t.Fatalf("events = %#v, err = %v", events, err)
	}
	response, body = doRequest(t, http.MethodGet, baseURL+"/v1/runs/run-2/events?after=1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events after status = %d: %s", response.StatusCode, body)
	}
	if err := json.Unmarshal(body, &events); err != nil || len(events) != 0 {
		t.Fatalf("events after = %#v, err = %v", events, err)
	}

	response, body = doRequest(t, http.MethodGet, baseURL+"/v1/runs/run-2/evidence", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("evidence status = %d: %s", response.StatusCode, body)
	}
	var evidence Evidence
	if err := json.Unmarshal(body, &evidence); err != nil || evidence.RunID != "run-2" || len(evidence.Manifest) == 0 {
		t.Fatalf("evidence = %#v, err = %v", evidence, err)
	}

	response, body = doRequest(t, http.MethodPost, baseURL+"/v1/runs/run-2/cancel", nil)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel status = %d: %s", response.StatusCode, body)
	}
	if err := json.Unmarshal(body, &status); err != nil || status.State != string(factory.StateCancelled) {
		t.Fatalf("cancelled status = %#v, err = %v", status, err)
	}
}
