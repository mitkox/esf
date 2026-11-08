package connector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/forge/azuredevops"
)

type fakeSweeper struct {
	sweep  func(context.Context) (forge.SweepResult, error)
	commit func(context.Context, string) error

	mu        sync.Mutex
	committed []string
}

func (f *fakeSweeper) Sweep(ctx context.Context) (forge.SweepResult, error) { return f.sweep(ctx) }

func (f *fakeSweeper) CommitSweep(ctx context.Context, cursor string) error {
	f.mu.Lock()
	f.committed = append(f.committed, cursor)
	f.mu.Unlock()
	if f.commit != nil {
		return f.commit(ctx, cursor)
	}
	return nil
}

func (f *fakeSweeper) committedCursors() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.committed...)
}

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
		Sweeper: &fakeSweeper{sweep: func(context.Context) (forge.SweepResult, error) {
			n := int(sweepCount.Add(1))
			sweepStarted <- n
			return forge.SweepResult{Items: []forge.Candidate{{
				Connector: "azdo",
				WorkItem:  forge.WorkItem{ID: "42", Revision: "1"},
			}}, NextCursor: "cursor-" + strconv.Itoa(n)}, nil
		}},
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
		Sweeper:       &fakeSweeper{sweep: func(context.Context) (forge.SweepResult, error) { return forge.SweepResult{}, nil }},
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
		Sweeper:        &fakeSweeper{sweep: func(context.Context) (forge.SweepResult, error) { return forge.SweepResult{}, nil }},
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
		{"missing handler", []Spec{func() Spec { s := valid; s.WebhookHandler = nil; return s }()}, "requires a webhook handler"},
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

func TestRuntimeReportsSweepAndFailedDeliveryHealth(t *testing.T) {
	t.Parallel()

	observations := make(chan HealthObservation, 8)
	var deliveryAttempts atomic.Int32
	runtime, err := New([]Spec{{
		Name:          "azdo",
		SweepInterval: time.Minute,
		Sweeper: &fakeSweeper{sweep: func(context.Context) (forge.SweepResult, error) {
			return forge.SweepResult{Items: []forge.Candidate{{Connector: "azdo", WorkItem: forge.WorkItem{ID: "42", Revision: "1"}}}, NextCursor: "cursor-1"}, nil
		}},
		Deliver: func(context.Context, forge.Candidate) error {
			if deliveryAttempts.Add(1) == 1 {
				return context.DeadlineExceeded
			}
			return nil
		},
		WebhookListen:  "127.0.0.1:0",
		WebhookHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Subscription: func(context.Context) (forge.SubscriptionState, string) {
			return forge.SubscriptionActive, "1 subscription enabled"
		},
		Observe: func(_ context.Context, observation HealthObservation) error {
			observations <- observation
			return nil
		},
	}}, Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	clock := &fakeTicker{ch: make(chan time.Time, 1)}
	runtime.newTicker = func(time.Duration) ticker { return clock }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	stopRuntimeOnCleanup(t, cancel, done)

	failedDelivery := nextObservation(t, observations)
	if failedDelivery.Event != HealthEventDelivery || failedDelivery.Successful {
		t.Fatalf("first observation = %#v, want the failed delivery before any sweep success", failedDelivery)
	}
	if !failedDelivery.LastSuccessfulSweep.IsZero() {
		t.Fatalf("a failed delivery advanced the sweep timestamp to %s", failedDelivery.LastSuccessfulSweep)
	}
	if !failedDelivery.LastDelivery.IsZero() {
		t.Fatal("a failed delivery advanced the delivery timestamp")
	}
	if failedDelivery.Detail == "" {
		t.Fatal("a failed delivery carried no detail")
	}
	if health := evaluateObservedHealth(time.Now().UTC(), failedDelivery); health.Status != forge.HealthDegraded || health.Reason != "delivery_failed" {
		t.Fatalf("failed delivery health = %s/%s, want degraded/delivery_failed", health.Status, health.Reason)
	}

	clock.ch <- time.Now()
	sweep := nextObservation(t, observations)
	if sweep.Event != HealthEventSweep || !sweep.Successful {
		t.Fatalf("second observation = %#v, want a successful sweep after the replayed delivery", sweep)
	}
	if sweep.LastSuccessfulSweep.IsZero() {
		t.Fatal("a successful sweep did not advance the sweep timestamp")
	}
	if sweep.Subscription != forge.SubscriptionActive || sweep.SubscriptionDetail == "" {
		t.Fatalf("sweep subscription = %q/%q", sweep.Subscription, sweep.SubscriptionDetail)
	}
	if !sweep.LastDelivery.IsZero() {
		t.Fatal("a sweep delivery advanced the delivery timestamp")
	}
}

// TestRuntimeReportsQueueDepthWithSweepObservation proves a sweep observation
// carries the durable pending-work count and the configured sweep interval.
func TestRuntimeReportsQueueDepthWithSweepObservation(t *testing.T) {
	t.Parallel()

	observations := make(chan HealthObservation, 4)
	runtime, err := New([]Spec{{
		Name:          "azdo",
		SweepInterval: time.Minute,
		Sweeper:       &fakeSweeper{sweep: func(context.Context) (forge.SweepResult, error) { return forge.SweepResult{}, nil }},
		Deliver:       func(context.Context, forge.Candidate) error { return nil },
		WebhookListen: "127.0.0.1:0",
		WebhookHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		}),
		QueueDepth: func(context.Context) (map[string]int, error) {
			return map[string]int{"payments": 3}, nil
		},
		Observe: func(_ context.Context, observation HealthObservation) error {
			observations <- observation
			return nil
		},
	}}, Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runtime.newTicker = func(time.Duration) ticker { return &fakeTicker{ch: make(chan time.Time, 1)} }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	sweep := <-observations
	if sweep.Event != HealthEventSweep || !sweep.Successful {
		cancel()
		waitRuntimeStop(t, done)
		t.Fatalf("observation = %#v, want a successful sweep", sweep)
	}
	if sweep.QueueDepth["payments"] != 3 {
		cancel()
		waitRuntimeStop(t, done)
		t.Fatalf("queue depth = %v, want the durable pending count", sweep.QueueDepth)
	}
	if sweep.SweepInterval != time.Minute {
		cancel()
		waitRuntimeStop(t, done)
		t.Fatalf("sweep interval = %s, want the configured interval", sweep.SweepInterval)
	}

	cancel()
	waitRuntimeStop(t, done)
}

func TestRuntimeReportsFailedWebhookDeliveryHealth(t *testing.T) {
	t.Parallel()

	observations := make(chan HealthObservation, 8)
	addresses := make(chan net.Addr, 1)
	runtime, err := New([]Spec{{
		Name:          "azdo",
		SweepInterval: time.Hour,
		Sweeper:       &fakeSweeper{sweep: func(context.Context) (forge.SweepResult, error) { return forge.SweepResult{}, nil }},
		Deliver:       func(context.Context, forge.Candidate) error { return nil },
		WebhookListen: "127.0.0.1:0",
		WebhookHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusBadGateway)
		}),
		Observe: func(_ context.Context, observation HealthObservation) error {
			observations <- observation
			return nil
		},
	}}, Options{
		Logger: quietLogger(),
		OnListen: func(_ string, address net.Addr, _ bool) {
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
		cancel()
		t.Fatalf("POST: %v", err)
	}
	response.Body.Close()

	for {
		observation := <-observations
		if observation.Event != HealthEventDelivery {
			continue
		}
		if observation.Successful {
			cancel()
			t.Fatalf("a 502 webhook was observed as successful: %#v", observation)
		}
		break
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
}

// TestRuntimeDoesNotCommitWatermarkAfterPartialDelivery proves a failure part
// way through a batch leaves the cursor unadvanced even though the earlier
// candidates were delivered.
func TestRuntimeDoesNotCommitWatermarkAfterPartialDelivery(t *testing.T) {
	t.Parallel()

	var sweepCalls atomic.Int32
	sweeper := &fakeSweeper{}
	sweeper.sweep = func(context.Context) (forge.SweepResult, error) {
		if sweepCalls.Add(1) == 1 {
			return forge.SweepResult{Items: []forge.Candidate{
				{WorkItem: forge.WorkItem{ID: "42", Revision: "1"}},
				{WorkItem: forge.WorkItem{ID: "43", Revision: "2"}},
			}, NextCursor: "cursor-1"}, nil
		}
		if cursors := sweeper.committedCursors(); len(cursors) != 0 {
			t.Errorf("committed cursors before the failed batch was replayed: %v", cursors)
		}
		return forge.SweepResult{NextCursor: "cursor-2"}, nil
	}
	attempted := make(chan string, 4)
	runtime, fakeClock := newSweepRuntime(t, sweeper, func(_ context.Context, candidate forge.Candidate) error {
		attempted <- candidate.WorkItem.ID
		if candidate.WorkItem.ID == "43" {
			return errors.New("delivery failed")
		}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	if id := <-attempted; id != "42" {
		t.Fatalf("first delivery = %s, want 42", id)
	}
	if id := <-attempted; id != "43" {
		t.Fatalf("second delivery = %s, want 43", id)
	}
	fakeClock.ch <- time.Now()

	deadline := time.Now().Add(5 * time.Second)
	for len(sweeper.committedCursors()) == 0 {
		if time.Now().After(deadline) {
			cancel()
			waitRuntimeStop(t, done)
			t.Fatal("the fully delivered second batch did not commit its cursor")
		}
		time.Sleep(time.Millisecond)
	}
	if cursors := sweeper.committedCursors(); len(cursors) != 1 || cursors[0] != "cursor-2" {
		t.Fatalf("committed cursors = %v, want only the fully delivered batch", cursors)
	}

	cancel()
	waitRuntimeStop(t, done)
}

// newSweepFixture builds the real Azure DevOps sweeper over a real file
// watermark, so a runtime test exercises the actual reconciliation loop rather
// than a stub. The feed returns one revision and then the last batch.
func newSweepFixture(t *testing.T) (*azuredevops.Sweeper, *azuredevops.FileWatermark) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/acme/_apis/wit/reporting/workitemrevisions":
			if token := r.URL.Query().Get("continuationToken"); token == "" || token == "before" {
				_, _ = io.WriteString(w, `{"values":[{"id":42,"rev":5}],"continuationToken":"after-1","isLastBatch":false}`)
				return
			}
			_, _ = io.WriteString(w, `{"values":[],"continuationToken":"","isLastBatch":true}`)
		case "/acme/_apis/wit/workitems/42":
			_, _ = io.WriteString(w, `{"id":42,"rev":5,"fields":{"System.TeamProject":"Payments","System.WorkItemType":"Bug","System.State":"Active","System.Title":"Payment retry loop spins forever","System.ChangedBy":{"id":"aad-1","descriptor":"aad.ada-lovelace"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	credentialPath := filepath.Join(t.TempDir(), "azdo.token")
	if err := os.WriteFile(credentialPath, []byte("esf:pat"), 0o600); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	credential, err := forge.ReadCredential(credentialPath)
	if err != nil {
		t.Fatalf("ReadCredential: %v", err)
	}
	client, err := azuredevops.NewClient(azuredevops.Config{
		Name:       "azdo",
		BaseURL:    server.URL + "/acme",
		Credential: credential,
		Retry:      azuredevops.RetryPolicy{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	watermark, err := azuredevops.NewFileWatermark(filepath.Join(t.TempDir(), "watermark"))
	if err != nil {
		t.Fatalf("NewFileWatermark: %v", err)
	}
	if err := watermark.Save(context.Background(), "before"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sweeper, err := azuredevops.NewSweeper(client, watermark, azuredevops.SweeperOptions{})
	if err != nil {
		t.Fatalf("NewSweeper: %v", err)
	}
	return sweeper, watermark
}

func newSweepRuntime(t *testing.T, sweeper Sweeper, deliver Deliverer) (*Runtime, *fakeTicker) {
	t.Helper()
	return newSweepRuntimeObserved(t, sweeper, deliver, nil)
}

func newSweepRuntimeObserved(t *testing.T, sweeper Sweeper, deliver Deliverer, observe HealthObserver) (*Runtime, *fakeTicker) {
	t.Helper()
	clock := &fakeTicker{ch: make(chan time.Time, 1)}
	runtime, err := New([]Spec{{
		Name:           "azdo",
		SweepInterval:  time.Minute,
		Sweeper:        sweeper,
		Deliver:        deliver,
		WebhookListen:  "127.0.0.1:0",
		WebhookHandler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		Observe:        observe,
	}}, Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runtime.newTicker = func(time.Duration) ticker { return clock }
	return runtime, clock
}

func nextObservation(t *testing.T, observations <-chan HealthObservation) HealthObservation {
	t.Helper()
	select {
	case observation := <-observations:
		return observation
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a health observation")
		return HealthObservation{}
	}
}

// evaluateObservedHealth projects a runtime observation into the health
// evaluation the connector workload performs on the same data.
func evaluateObservedHealth(now time.Time, observation HealthObservation) forge.ConnectorHealth {
	return forge.EvaluateHealth(now, forge.HealthObservation{
		Connector:           observation.Connector,
		SweepInterval:       observation.SweepInterval,
		Event:               observation.Event,
		Successful:          observation.Successful,
		Detail:              observation.Detail,
		LastSuccessfulSweep: observation.LastSuccessfulSweep,
		LastDelivery:        observation.LastDelivery,
		Subscription:        observation.Subscription,
		SubscriptionDetail:  observation.SubscriptionDetail,
		QueueDepth:          observation.QueueDepth,
	})
}

func waitRuntimeStop(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime did not stop after cancellation")
	}
}

// stopRuntimeOnCleanup cancels the runtime and waits for it when the test ends,
// including assertion-failure paths that would otherwise leave it running.
func stopRuntimeOnCleanup(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop after cancellation")
		}
	})
}

// TestRuntimeReplaysFailedSweepDelivery proves a failed delivery leaves the
// stored watermark unadvanced, so the next tick re-emits the same revision, and
// only a fully delivered batch commits the cursor.
func TestRuntimeReplaysFailedSweepDelivery(t *testing.T) {
	t.Parallel()

	sweeper, watermark := newSweepFixture(t)
	var deliveries atomic.Int32
	attempted := make(chan int, 4)
	runtime, fakeClock := newSweepRuntime(t, sweeper, func(_ context.Context, candidate forge.Candidate) error {
		if candidate.WorkItem.ID != "42" || candidate.WorkItem.Revision != "5" {
			t.Errorf("candidate = %s rev %s, want work item 42 rev 5", candidate.WorkItem.ID, candidate.WorkItem.Revision)
		}
		n := int(deliveries.Add(1))
		attempted <- n
		if n == 1 {
			return errors.New("intake unavailable")
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	if n := <-attempted; n != 1 {
		t.Fatalf("first delivery attempt = %d", n)
	}
	stored, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "before" {
		t.Fatalf("watermark after a failed delivery = %q, want the unadvanced before", stored)
	}

	fakeClock.ch <- time.Now()
	select {
	case n := <-attempted:
		if n != 2 {
			t.Fatalf("replayed delivery attempt = %d, want 2", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failed sweep batch was not re-emitted on the next tick")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		stored, err = watermark.Load(context.Background())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if stored == "after-1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watermark after the replayed delivery = %q, want after-1", stored)
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	waitRuntimeStop(t, done)
}

// TestRuntimeFailedDeliveryKeepsWatermarkAndDegradesHealth proves a failed
// candidate delivery leaves the durable watermark unadvanced and records a
// failed delivery observation that health evaluates as degraded, instead of
// recording a successful sweep for the same pass.
func TestRuntimeFailedDeliveryKeepsWatermarkAndDegradesHealth(t *testing.T) {
	t.Parallel()

	sweeper, watermark := newSweepFixture(t)
	observations := make(chan HealthObservation, 4)
	attempted := make(chan struct{}, 1)
	runtime, _ := newSweepRuntimeObserved(t, sweeper, func(context.Context, forge.Candidate) error {
		attempted <- struct{}{}
		return errors.New("intake unavailable")
	}, func(_ context.Context, observation HealthObservation) error {
		observations <- observation
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	stopRuntimeOnCleanup(t, cancel, done)

	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery was not attempted")
	}
	observation := nextObservation(t, observations)
	if observation.Event != HealthEventDelivery || observation.Successful {
		t.Fatalf("observation = %#v, want a failed delivery", observation)
	}
	if !observation.LastSuccessfulSweep.IsZero() {
		t.Fatalf("a failed delivery batch recorded a successful sweep at %s", observation.LastSuccessfulSweep)
	}
	if observation.Detail == "" {
		t.Fatal("a failed delivery carried no detail")
	}
	if health := evaluateObservedHealth(time.Now().UTC(), observation); health.Status != forge.HealthDegraded || health.Reason != "delivery_failed" {
		t.Fatalf("failed delivery health = %s/%s, want degraded/delivery_failed", health.Status, health.Reason)
	}

	stored, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "before" {
		t.Fatalf("watermark after a failed delivery = %q, want the unadvanced before", stored)
	}
}

// TestRuntimeCommitSweepFailureDegradesHealth proves a watermark commit failure
// is recorded as a failed sweep with its bounded detail instead of advancing
// the successful-sweep timestamp.
func TestRuntimeCommitSweepFailureDegradesHealth(t *testing.T) {
	t.Parallel()

	observations := make(chan HealthObservation, 4)
	sweeper := &fakeSweeper{
		sweep: func(context.Context) (forge.SweepResult, error) {
			return forge.SweepResult{NextCursor: "cursor-1"}, nil
		},
		commit: func(context.Context, string) error { return errors.New("watermark store unavailable") },
	}
	runtime, _ := newSweepRuntimeObserved(t, sweeper, func(context.Context, forge.Candidate) error { return nil }, func(_ context.Context, observation HealthObservation) error {
		observations <- observation
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	stopRuntimeOnCleanup(t, cancel, done)

	observation := nextObservation(t, observations)
	if observation.Event != HealthEventSweep || observation.Successful {
		t.Fatalf("observation = %#v, want a failed sweep for the commit error", observation)
	}
	if !observation.LastSuccessfulSweep.IsZero() {
		t.Fatalf("a failed commit advanced the sweep timestamp to %s", observation.LastSuccessfulSweep)
	}
	if !strings.Contains(observation.Detail, "sweep commit failed") {
		t.Fatalf("commit failure detail = %q, want it to name the commit failure", observation.Detail)
	}
	health := evaluateObservedHealth(time.Now().UTC(), observation)
	if health.Status != forge.HealthDegraded || health.Reason != "sweep_failed" {
		t.Fatalf("commit failure health = %s/%s, want degraded/sweep_failed", health.Status, health.Reason)
	}
	if cursors := sweeper.committedCursors(); len(cursors) != 1 || cursors[0] != "cursor-1" {
		t.Fatalf("commit attempts = %v, want the failed cursor attempt", cursors)
	}
}

// TestRuntimeCancelledDeliveryLeavesWatermarkUnadvanced proves a delivery
// interrupted by shutdown does not commit the batch cursor.
func TestRuntimeCancelledDeliveryLeavesWatermarkUnadvanced(t *testing.T) {
	t.Parallel()

	sweeper, watermark := newSweepFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	attempted := make(chan struct{}, 1)
	runtime, _ := newSweepRuntime(t, sweeper, func(deliveryCtx context.Context, _ forge.Candidate) error {
		attempted <- struct{}{}
		cancel()
		return deliveryCtx.Err()
	})

	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()

	select {
	case <-attempted:
	case <-time.After(5 * time.Second):
		cancel()
		waitRuntimeStop(t, done)
		t.Fatal("delivery was not attempted")
	}
	waitRuntimeStop(t, done)

	stored, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "before" {
		t.Fatalf("watermark after a cancelled delivery = %q, want the unadvanced before", stored)
	}
}

func TestRuntimeRunsSweepOnlyConnectorWithoutListener(t *testing.T) {
	t.Parallel()

	swept := make(chan struct{}, 1)
	runtime, err := New([]Spec{{
		Name:          "azdo",
		SweepInterval: time.Hour,
		Sweeper: &fakeSweeper{sweep: func(context.Context) (forge.SweepResult, error) {
			select {
			case swept <- struct{}{}:
			default:
			}
			return forge.SweepResult{}, nil
		}},
		Deliver: func(context.Context, forge.Candidate) error { return nil },
	}}, Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	select {
	case <-swept:
	case <-time.After(5 * time.Second):
		t.Fatal("sweep-only connector did not run its first sweep")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
