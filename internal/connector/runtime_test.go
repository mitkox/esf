package connector

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

type fakeSweeper func(context.Context) ([]forge.Candidate, error)

func (f fakeSweeper) Sweep(ctx context.Context) ([]forge.Candidate, error) { return f(ctx) }

type fakeTicker struct {
	ch      chan time.Time
	stopped atomic.Bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.stopped.Store(true) }

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRuntimeSweepsOnScheduleUntilShutdown(t *testing.T) {
	t.Parallel()

	var delivered atomic.Int32
	var sweepCount atomic.Int32
	sweepStarted := make(chan int, 4)
	fakeClock := &fakeTicker{ch: make(chan time.Time, 1)}
	runtime, err := New([]Spec{{
		Name:          "azdo",
		SweepInterval: time.Minute,
		Sweeper: fakeSweeper(func(context.Context) ([]forge.Candidate, error) {
			n := int(sweepCount.Add(1))
			sweepStarted <- n
			return []forge.Candidate{{
				Connector: "azdo",
				WorkItem:  forge.WorkItem{ID: "42", Revision: "1"},
			}}, nil
		}),
		Deliver: func(context.Context, forge.Candidate) error {
			delivered.Add(1)
			return nil
		},
		WebhookListen:  "127.0.0.1:0",
		WebhookHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}}, Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runtime.newTicker = func(time.Duration) ticker { return fakeClock }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	if first := <-sweepStarted; first != 1 {
		t.Fatalf("first sweep = %d", first)
	}
	fakeClock.ch <- time.Now()
	if second := <-sweepStarted; second != 2 {
		t.Fatalf("second sweep = %d", second)
	}
	if got := delivered.Load(); got != 2 {
		t.Fatalf("delivered = %d, want 2", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop after cancellation")
	}
	if !fakeClock.stopped.Load() {
		t.Fatal("sweep ticker was not stopped")
	}
}

func TestRuntimeServesWebhookUntilShutdown(t *testing.T) {
	t.Parallel()

	addresses := make(chan net.Addr, 1)
	runtime, err := New([]Spec{{
		Name:          "azdo",
		SweepInterval: time.Hour,
		Sweeper:       fakeSweeper(func(context.Context) ([]forge.Candidate, error) { return nil, nil }),
		Deliver:       func(context.Context, forge.Candidate) error { return nil },
		WebhookListen: "127.0.0.1:0",
		WebhookHandler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/hooks/azuredevops" {
				http.NotFound(response, request)
				return
			}
			response.WriteHeader(http.StatusNoContent)
		}),
	}}, Options{
		Logger: quietLogger(),
		OnListen: func(name string, address net.Addr, tlsEnabled bool) {
			if name != "azdo" || tlsEnabled {
				t.Errorf("OnListen(%q, %v)", name, tlsEnabled)
			}
			addresses <- address
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	address := <-addresses
	response, err := http.Post("http://"+address.String()+"/hooks/azuredevops", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop after cancellation")
	}
	if connection, err := net.DialTimeout("tcp", address.String(), 200*time.Millisecond); err == nil {
		connection.Close()
		t.Fatal("webhook listener stayed open after shutdown")
	}
}

func TestRuntimeRejectsInvalidSpecs(t *testing.T) {
	t.Parallel()

	valid := Spec{
		Name:           "azdo",
		SweepInterval:  time.Minute,
		Sweeper:        fakeSweeper(func(context.Context) ([]forge.Candidate, error) { return nil, nil }),
		Deliver:        func(context.Context, forge.Candidate) error { return nil },
		WebhookListen:  "127.0.0.1:8091",
		WebhookHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}
	cases := []struct {
		name  string
		specs []Spec
		want  string
	}{
		{"no connectors", nil, "at least one"},
		{"missing name", []Spec{func() Spec { s := valid; s.Name = " "; return s }()}, "name is required"},
		{"duplicate name", []Spec{valid, valid}, "configured twice"},
		{"zero interval", []Spec{func() Spec { s := valid; s.SweepInterval = 0; return s }()}, "interval must be positive"},
		{"missing sweeper", []Spec{func() Spec { s := valid; s.Sweeper = nil; return s }()}, "sweeper is required"},
		{"missing deliverer", []Spec{func() Spec { s := valid; s.Deliver = nil; return s }()}, "deliverer is required"},
		{"missing handler", []Spec{func() Spec { s := valid; s.WebhookHandler = nil; return s }()}, "webhook handler is required"},
		{"missing listen", []Spec{func() Spec { s := valid; s.WebhookListen = ""; return s }()}, "listen address is required"},
		{"plaintext remote listen", []Spec{func() Spec { s := valid; s.WebhookListen = "0.0.0.0:8091"; return s }()}, "non-loopback"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(tc.specs, Options{Logger: quietLogger()}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}
