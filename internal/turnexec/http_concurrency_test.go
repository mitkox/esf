package turnexec

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type stalledBody struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *stalledBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return 0, io.EOF
}
func (b *stalledBody) Close() error { return nil }

type stalledResponse struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *stalledResponse) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func completedHTTPService() *Service {
	req := request("one")
	req.OwnerToken = ""
	return &Service{
		token:    serviceToken,
		profiles: profiles(),
		records: map[string]*record{
			"one": {
				Status:      Status{ExecutionID: "one", State: "completed"},
				OwnerHash:   digest([]byte(ownerToken)),
				Fingerprint: digestJSON(req),
				Events:      []Event{{Seq: 1, Type: "state", Data: map[string]any{"status": "completed"}}},
				Result:      &Result{Result: map[string]any{"text": "done"}},
			},
		},
	}
}

func requireConcurrentStatus(t *testing.T, s *Service) {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(s, "GET", "executions/one", nil) }()
	select {
	case result := <-done:
		if result.Code != http.StatusOK {
			t.Fatalf("status returned %d: %s", result.Code, result.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stalled HTTP I/O blocked unrelated execution status")
	}
}

func TestStalledMutationBodyDoesNotHoldServiceLock(t *testing.T) {
	for _, operation := range []string{"cancel", "renew", "permissions"} {
		t.Run(operation, func(t *testing.T) {
			s := completedHTTPService()
			body := &stalledBody{entered: make(chan struct{}), release: make(chan struct{})}
			req := httptest.NewRequest("POST", "/v1/executions/one/"+operation, body)
			req.Header.Set("Authorization", "Bearer "+serviceToken)
			done := make(chan struct{})
			go func() { defer close(done); s.ServeHTTP(httptest.NewRecorder(), req) }()
			defer func() { close(body.release); <-done }()
			select {
			case <-body.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not start reading request body")
			}
			requireConcurrentStatus(t, s)
		})
	}
}

func TestStalledResponseDoesNotHoldServiceLock(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		payload            any
	}{
		{"status", "GET", "executions/one", nil},
		{"events", "GET", "executions/one/events", nil},
		{"result", "GET", "executions/one/result", nil},
		{"cancel", "POST", "executions/one/cancel", map[string]string{"owner_token": ownerToken}},
		{"renew", "POST", "executions/one/renew", map[string]string{"owner_token": ownerToken}},
		{"permissions", "POST", "executions/one/permissions", map[string]string{"owner_token": ownerToken, "decision": "allow"}},
		{"idempotent_submit", "POST", "executions", request("one")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := completedHTTPService()
			payload, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tc.method, "/v1/"+tc.path, bytes.NewReader(payload))
			req.Header.Set("Authorization", "Bearer "+serviceToken)
			response := &stalledResponse{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
			done := make(chan struct{})
			go func() { defer close(done); s.ServeHTTP(response, req) }()
			defer func() { close(response.release); <-done }()
			select {
			case <-response.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("handler did not start writing response")
			}
			requireConcurrentStatus(t, s)
		})
	}
}
