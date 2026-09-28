package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// funcNotifier adapts a function to the Notifier interface for tests.
type funcNotifier func(context.Context, Event) error

func (f funcNotifier) Notify(ctx context.Context, e Event) error { return f(ctx, e) }

type capturedRequest struct {
	method string
	header http.Header
	body   []byte
}

// recordingServer returns a test server that hands every request to the test
// over a buffered channel.
func recordingServer(t *testing.T) (*httptest.Server, <-chan capturedRequest) {
	t.Helper()
	requests := make(chan capturedRequest, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- capturedRequest{method: r.Method, header: r.Header.Clone(), body: body}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

func recvRequest(t *testing.T, ch <-chan capturedRequest) capturedRequest {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for webhook request")
		return capturedRequest{}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestNewWebhookValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WebhookConfig
		wantErr string // substring; empty means no error
	}{
		{name: "http ok", cfg: WebhookConfig{URL: "http://example.com/hook"}},
		{name: "https ok with token", cfg: WebhookConfig{URL: "https://example.com/hook", Token: "t0ken"}},
		{name: "http with port ok", cfg: WebhookConfig{URL: "http://127.0.0.1:8080/hook"}},
		{name: "empty url", cfg: WebhookConfig{URL: ""}, wantErr: "scheme"},
		{name: "no scheme", cfg: WebhookConfig{URL: "example.com/hook"}, wantErr: "scheme"},
		{name: "unsupported scheme", cfg: WebhookConfig{URL: "ftp://example.com/hook"}, wantErr: "scheme"},
		{name: "file scheme", cfg: WebhookConfig{URL: "file:///etc/passwd"}, wantErr: "scheme"},
		{name: "no host", cfg: WebhookConfig{URL: "https:///hook"}, wantErr: "host"},
		{name: "unparseable", cfg: WebhookConfig{URL: "http://[::1"}, wantErr: "invalid webhook URL"},
		{name: "userinfo rejected", cfg: WebhookConfig{URL: "https://user:pass@example.com/hook"}, wantErr: "userinfo"},
		{name: "user only rejected", cfg: WebhookConfig{URL: "http://user@example.com/hook"}, wantErr: "userinfo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := NewWebhook(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("NewWebhook(%q) = %v, want nil", tt.cfg.URL, err)
				}
				if w == nil {
					t.Fatal("NewWebhook returned nil webhook with nil error")
				}
				return
			}
			if err == nil {
				t.Fatalf("NewWebhook(%q) = nil error, want error containing %q", tt.cfg.URL, tt.wantErr)
			}
			if w != nil {
				t.Fatalf("NewWebhook(%q) returned non-nil webhook alongside error", tt.cfg.URL)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewWebhookDefaultsAndOverrides(t *testing.T) {
	def, err := NewWebhook(WebhookConfig{URL: "https://example.com/hook"})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if def.timeout != defaultTimeout {
		t.Errorf("timeout = %v, want %v", def.timeout, defaultTimeout)
	}
	if def.maxDetailBytes != defaultMaxDetailBytes {
		t.Errorf("maxDetailBytes = %d, want %d", def.maxDetailBytes, defaultMaxDetailBytes)
	}
	if def.client == nil || def.client.Timeout != defaultTimeout {
		t.Errorf("default client timeout not applied: %+v", def.client)
	}

	custom, err := NewWebhook(WebhookConfig{
		URL:            "https://example.com/hook",
		Timeout:        2 * time.Second,
		MaxDetailBytes: 128,
	})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if custom.timeout != 2*time.Second {
		t.Errorf("timeout = %v, want 2s", custom.timeout)
	}
	if custom.maxDetailBytes != 128 {
		t.Errorf("maxDetailBytes = %d, want 128", custom.maxDetailBytes)
	}

	negative, err := NewWebhook(WebhookConfig{URL: "https://example.com/hook", Timeout: -1, MaxDetailBytes: -5})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if negative.timeout != defaultTimeout || negative.maxDetailBytes != defaultMaxDetailBytes {
		t.Errorf("negative config did not fall back to defaults: %v, %d", negative.timeout, negative.maxDetailBytes)
	}
}

func TestNewWebhookDoesNotMutateInjectedClient(t *testing.T) {
	injected := &http.Client{Timeout: 42 * time.Second}
	if _, err := NewWebhook(WebhookConfig{URL: "https://example.com/hook", Client: injected}); err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if injected.CheckRedirect != nil {
		t.Error("NewWebhook mutated the caller's http.Client CheckRedirect")
	}
	if injected.Timeout != 42*time.Second {
		t.Errorf("NewWebhook mutated the caller's http.Client Timeout: %v", injected.Timeout)
	}
}

func TestWebhookNotifySuccess(t *testing.T) {
	srv, requests := recordingServer(t)
	w, err := NewWebhook(WebhookConfig{URL: srv.URL + "/hook", Token: "s3cr3t"})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}

	want := Event{
		Kind:     "run.quarantined",
		Severity: SeverityCritical,
		RunID:    "run-123",
		Summary:  "behavior monitor quarantined the run",
		Detail:   map[string]string{"reason": "egress violation", "rule": "R-7"},
		At:       time.Date(2026, 5, 4, 3, 2, 1, 0, time.UTC),
	}
	if err := w.Notify(context.Background(), want); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	got := recvRequest(t, requests)
	if got.method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.method)
	}
	if ct := got.header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if auth := got.header.Get("Authorization"); auth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer s3cr3t")
	}
	if !json.Valid(got.body) {
		t.Fatalf("body is not valid JSON: %q", got.body)
	}

	var decoded Event
	if err := json.Unmarshal(got.body, &decoded); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if decoded.Kind != want.Kind || decoded.Severity != want.Severity || decoded.RunID != want.RunID || decoded.Summary != want.Summary {
		t.Errorf("decoded event = %+v, want %+v", decoded, want)
	}
	if !decoded.At.Equal(want.At) {
		t.Errorf("At = %v, want %v", decoded.At, want.At)
	}
	if !reflect.DeepEqual(decoded.Detail, want.Detail) {
		t.Errorf("Detail = %v, want %v", decoded.Detail, want.Detail)
	}
}

func TestWebhookErrorDoesNotEchoBearerToken(t *testing.T) {
	const token = "reflected-secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("rejected " + r.Header.Get("Authorization")))
	}))
	defer srv.Close()
	notifier, err := NewWebhook(WebhookConfig{URL: srv.URL, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	err = notifier.Notify(context.Background(), Event{Kind: "run.quarantined"})
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("webhook error exposed bearer token: %v", err)
	}
}

func TestWebhookNotifyOmitsBearerWhenUnconfigured(t *testing.T) {
	srv, requests := recordingServer(t)
	w, err := NewWebhook(WebhookConfig{URL: srv.URL + "/hook"})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if err := w.Notify(context.Background(), Event{Kind: "k", Severity: SeverityInfo, Summary: "s", At: time.Now()}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	got := recvRequest(t, requests)
	if _, present := got.header["Authorization"]; present {
		t.Errorf("Authorization header present without a configured token: %q", got.header.Get("Authorization"))
	}
	// Empty Detail must be omitted rather than serialized as null.
	if strings.Contains(string(got.body), `"detail"`) {
		t.Errorf("empty Detail was serialized: %s", got.body)
	}
}

func TestWebhookNotifyNon2xxReturnsBoundedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom: " + strings.Repeat("x", 5000)))
	}))
	defer srv.Close()

	w, err := NewWebhook(WebhookConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	err = w.Notify(context.Background(), Event{Kind: "k", Summary: "s", At: time.Now()})
	if err == nil {
		t.Fatal("Notify returned nil for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not contain the status code", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not contain the response body", err)
	}
	if len(err.Error()) > 2048 {
		t.Errorf("error length %d is not bounded", len(err.Error()))
	}
	if strings.Count(err.Error(), "x") > maxErrBodyBytes {
		t.Errorf("error echoed more than the bounded body: %d x's", strings.Count(err.Error(), "x"))
	}
}

func TestWebhookNotifyHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	w, err := NewWebhook(WebhookConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}

	t.Run("pre-cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := w.Notify(ctx, Event{Kind: "k", Summary: "s", At: time.Now()})
		if err == nil {
			t.Fatal("Notify returned nil with a cancelled context")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error %v does not wrap context.Canceled", err)
		}
	})

	t.Run("deadline exceeds configured timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := w.Notify(ctx, Event{Kind: "k", Summary: "s", At: time.Now()})
		if err == nil {
			t.Fatal("Notify returned nil after the context deadline expired")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error %v does not wrap context.DeadlineExceeded", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("Notify ignored the context deadline, took %v", elapsed)
		}
	})

	t.Run("configured timeout bounds delivery", func(t *testing.T) {
		short, err := NewWebhook(WebhookConfig{URL: srv.URL, Timeout: 50 * time.Millisecond})
		if err != nil {
			t.Fatalf("NewWebhook: %v", err)
		}
		start := time.Now()
		if err := short.Notify(context.Background(), Event{Kind: "k", Summary: "s", At: time.Now()}); err == nil {
			t.Fatal("Notify returned nil despite the configured timeout")
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("Notify ignored the configured timeout, took %v", elapsed)
		}
	})
}

func TestWebhookRejectsCrossHostRedirect(t *testing.T) {
	var hits int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// Force a genuine hostname change (127.0.0.1 -> localhost), which still
	// resolves to the target so following it would really deliver the token.
	otherHost := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherHost+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	w, err := NewWebhook(WebhookConfig{URL: redirector.URL, Token: "s3cr3t"})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	err = w.Notify(context.Background(), Event{Kind: "k", Summary: "s", At: time.Now()})
	if err == nil {
		t.Fatal("Notify followed a cross-host redirect; want an error")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Errorf("error %q does not mention the refused redirect", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("cross-host target was contacted %d times, want 0", got)
	}
}

func TestWebhookFollowsSameHostRedirect(t *testing.T) {
	var final int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		atomic.AddInt32(&final, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w, err := NewWebhook(WebhookConfig{URL: srv.URL + "/start", Token: "s3cr3t"})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if err := w.Notify(context.Background(), Event{Kind: "k", Summary: "s", At: time.Now()}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if got := atomic.LoadInt32(&final); got != 1 {
		t.Errorf("same-host redirect landed %d times, want 1", got)
	}
}

func TestWebhookBoundsHugeDetail(t *testing.T) {
	const maxDetail = 256
	srv, requests := recordingServer(t)
	w, err := NewWebhook(WebhookConfig{URL: srv.URL, MaxDetailBytes: maxDetail})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}

	huge := strings.Repeat("A", 100_000)
	e := Event{
		Kind:     "sandbox.leak",
		Severity: SeverityCritical,
		Summary:  "sandbox escaped its microVM",
		Detail: map[string]string{
			"blob":      huge,
			"a_short":   "ok",
			"z_suffix":  "tail",
			"multibyte": strings.Repeat("é", 50_000),
		},
		At: time.Now(),
	}
	if err := w.Notify(context.Background(), e); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	got := recvRequest(t, requests)

	if !json.Valid(got.body) {
		t.Fatalf("truncated body is not valid JSON (%d bytes)", len(got.body))
	}
	// Envelope (summary + kind + severity + timestamp) plus the detail budget
	// must stay in a sane band; the 100 KB value must not survive.
	if len(got.body) > maxDetail+4096 {
		t.Fatalf("delivered body is %d bytes, want <= %d", len(got.body), maxDetail+4096)
	}
	if strings.Contains(string(got.body), huge) {
		t.Fatal("delivered body contains the untruncated detail value")
	}

	var decoded Event
	if err := json.Unmarshal(got.body, &decoded); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if decoded.Detail == nil {
		t.Fatal("all detail was dropped; want a bounded subset")
	}
	if serialized := len(mustJSON(t, decoded.Detail)); serialized > maxDetail {
		t.Errorf("serialized Detail is %d bytes, want <= %d", serialized, maxDetail)
	}
	if decoded.Detail["a_short"] != "ok" {
		t.Errorf("small value a_short = %q, want %q", decoded.Detail["a_short"], "ok")
	}
	blob, ok := decoded.Detail["blob"]
	if !ok {
		t.Fatal("truncated key blob is missing")
	}
	if !strings.HasSuffix(blob, truncationSuffix) {
		t.Errorf("truncated value does not end in %q: %q", truncationSuffix, blob)
	}
	if len(blob) >= len(huge) {
		t.Errorf("blob was not shortened: %d bytes", len(blob))
	}
	if mb, ok := decoded.Detail["multibyte"]; ok {
		if !utf8.ValidString(mb) {
			t.Error("truncated multibyte value is not valid UTF-8")
		}
		if !strings.HasSuffix(mb, truncationSuffix) {
			t.Errorf("truncated multibyte value does not end in %q", truncationSuffix)
		}
	}
}

func TestWebhookBoundsHugeDetailWithDefaults(t *testing.T) {
	srv, requests := recordingServer(t)
	w, err := NewWebhook(WebhookConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	if err := w.Notify(context.Background(), Event{
		Kind:    "egress.enforcement_failed",
		Summary: "egress enforcement failed",
		Detail:  map[string]string{"dump": strings.Repeat("z", 1_000_000)},
		At:      time.Now(),
	}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	got := recvRequest(t, requests)
	if !json.Valid(got.body) {
		t.Fatal("truncated body is not valid JSON")
	}
	if len(got.body) > defaultMaxDetailBytes+4096 {
		t.Errorf("delivered body is %d bytes, want <= %d", len(got.body), defaultMaxDetailBytes+4096)
	}
	var decoded Event
	if err := json.Unmarshal(got.body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if serialized := len(mustJSON(t, decoded.Detail)); serialized > defaultMaxDetailBytes {
		t.Errorf("serialized Detail is %d bytes, want <= %d", serialized, defaultMaxDetailBytes)
	}
}

func TestWebhookBoundsHugeSummary(t *testing.T) {
	srv, requests := recordingServer(t)
	w, err := NewWebhook(WebhookConfig{URL: srv.URL})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	huge := strings.Repeat("S", 20_000)
	if err := w.Notify(context.Background(), Event{Kind: "agent.blocked", Summary: huge, At: time.Now()}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	got := recvRequest(t, requests)
	if !json.Valid(got.body) {
		t.Fatal("body is not valid JSON")
	}
	var decoded Event
	if err := json.Unmarshal(got.body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Summary) > maxSummaryBytes {
		t.Errorf("summary is %d bytes, want <= %d", len(decoded.Summary), maxSummaryBytes)
	}
	if !strings.HasSuffix(decoded.Summary, truncationSuffix) {
		t.Errorf("truncated summary does not end in %q", truncationSuffix)
	}
}

func TestWebhookConcurrentNotify(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	w, err := NewWebhook(WebhookConfig{URL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}

	const goroutines, perGoroutine = 16, 10
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				e := Event{
					Kind:     "concurrent",
					Severity: SeverityWarning,
					Summary:  "concurrent delivery",
					Detail:   map[string]string{"worker": string(rune('a' + g%26))},
					At:       time.Now(),
				}
				if err := w.Notify(context.Background(), e); err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Notify: %v", err)
	}
	if got := atomic.LoadInt64(&calls); got != goroutines*perGoroutine {
		t.Errorf("server saw %d calls, want %d", got, goroutines*perGoroutine)
	}
}

func TestMultiAttemptsAllAndJoinsErrors(t *testing.T) {
	errFirst := errors.New("first failure")
	errSecond := errors.New("second failure")

	var firstCalls, secondCalls, okCalls int32
	m := Multi{
		funcNotifier(func(context.Context, Event) error {
			atomic.AddInt32(&firstCalls, 1)
			return errFirst
		}),
		funcNotifier(func(context.Context, Event) error {
			atomic.AddInt32(&secondCalls, 1)
			return errSecond
		}),
		funcNotifier(func(context.Context, Event) error {
			atomic.AddInt32(&okCalls, 1)
			return nil
		}),
	}

	err := m.Notify(context.Background(), Event{Kind: "k", Summary: "s"})
	if err == nil {
		t.Fatal("Multi.Notify returned nil, want joined errors")
	}
	if !errors.Is(err, errFirst) {
		t.Errorf("joined error does not wrap the first failure: %v", err)
	}
	if !errors.Is(err, errSecond) {
		t.Errorf("joined error does not wrap the second failure: %v", err)
	}
	if !strings.Contains(err.Error(), "first failure") || !strings.Contains(err.Error(), "second failure") {
		t.Errorf("joined error %q is missing a failure message", err)
	}
	if firstCalls != 1 || secondCalls != 1 || okCalls != 1 {
		t.Errorf("calls = (%d, %d, %d), want every notifier attempted once", firstCalls, secondCalls, okCalls)
	}
}

func TestMultiSkipsNil(t *testing.T) {
	var calls int32
	m := Multi{
		nil,
		funcNotifier(func(context.Context, Event) error {
			atomic.AddInt32(&calls, 1)
			return nil
		}),
		nil,
	}
	if err := m.Notify(context.Background(), Event{Kind: "k", Summary: "s"}); err != nil {
		t.Fatalf("Multi.Notify: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}

	// A typed-nil notifier must not panic either.
	var typedNil *Webhook
	if err := (Multi{typedNil}).Notify(context.Background(), Event{Kind: "k"}); err == nil {
		t.Error("typed-nil *Webhook returned nil error, want an error (and no panic)")
	}

	if err := (Multi{}).Notify(context.Background(), Event{Kind: "k"}); err != nil {
		t.Errorf("empty Multi.Notify = %v, want nil", err)
	}
}

func TestNopNotify(t *testing.T) {
	if err := (Nop{}).Notify(context.Background(), Event{Kind: "k", Summary: "s"}); err != nil {
		t.Fatalf("Nop.Notify = %v, want nil", err)
	}
}

func TestBoundDetailUnit(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := boundDetail(nil, 128); got != nil {
			t.Errorf("boundDetail(nil) = %v, want nil", got)
		}
	})
	t.Run("no budget", func(t *testing.T) {
		if got := boundDetail(map[string]string{"k": "v"}, 1); got != nil {
			t.Errorf("boundDetail with 1 byte budget = %v, want nil", got)
		}
	})
	t.Run("fits unchanged", func(t *testing.T) {
		in := map[string]string{"a": "1", "b": "2"}
		got := boundDetail(in, 1024)
		if !reflect.DeepEqual(got, in) {
			t.Errorf("boundDetail = %v, want %v", got, in)
		}
	})
	t.Run("huge values stay within budget", func(t *testing.T) {
		const max = 128
		in := map[string]string{
			"k1": strings.Repeat("x", 10_000),
			"k2": strings.Repeat("<&>\"\n", 5_000),
			"k3": "tail",
		}
		got := boundDetail(in, max)
		if serialized := len(mustJSON(t, got)); serialized > max {
			t.Errorf("serialized bound detail = %d bytes, want <= %d", serialized, max)
		}
		for k, v := range got {
			if !utf8.ValidString(v) {
				t.Errorf("value for %q is not valid UTF-8", k)
			}
		}
	})
	t.Run("deterministic", func(t *testing.T) {
		in := map[string]string{"b": strings.Repeat("b", 500), "a": strings.Repeat("a", 500), "c": "c"}
		first := boundDetail(in, 200)
		for i := 0; i < 20; i++ {
			if got := boundDetail(in, 200); !reflect.DeepEqual(got, first) {
				t.Fatalf("boundDetail is not deterministic: %v vs %v", got, first)
			}
		}
	})
}

func TestTruncateUnit(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 10, "short"},
		{"abcdef", 6, "abcdef"},
		{"abcdef", 5, "ab..."},
		{"abcdef", 3, "..."},
		{"abcdef", 1, "."},
		{"abcdef", 0, ""},
		{"héllo wörld", 8, "héll..."},
	}
	for _, tt := range tests {
		got := truncate(tt.in, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
		if len(got) > tt.max {
			t.Errorf("truncate(%q, %d) = %q has %d bytes", tt.in, tt.max, got, len(got))
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q is not valid UTF-8", tt.in, tt.max, got)
		}
	}
}

func TestReadBounded(t *testing.T) {
	t.Run("shorter than limit", func(t *testing.T) {
		if got := readBounded(strings.NewReader("hello"), 512); got != "hello" {
			t.Errorf("readBounded = %q, want hello", got)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		got := readBounded(strings.NewReader(strings.Repeat("x", 5000)), 16)
		if len(got) != 16+len(truncationSuffix) {
			t.Errorf("readBounded length = %d, want %d", len(got), 16+len(truncationSuffix))
		}
		if !strings.HasSuffix(got, truncationSuffix) {
			t.Errorf("readBounded = %q, want an ellipsis marker", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := readBounded(strings.NewReader(""), 16); got != "" {
			t.Errorf("readBounded = %q, want empty", got)
		}
	})
}
