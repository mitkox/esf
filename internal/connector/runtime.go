// Package connector runs the host-side forge connector workload.
//
// A connector process serves one authenticated webhook receiver per connector
// and reconciles each connector on its own interval. The runtime owns only the
// transport and scheduling: authentication, payload bounds, deduplication and
// re-fetching live in the forge adapter, and the intake decision lives behind
// the sink.
package connector

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/httpsecurity"
)

const (
	webhookReadHeaderTimeout = 5 * time.Second
	webhookReadTimeout       = 30 * time.Second
	webhookWriteTimeout      = 30 * time.Second
	webhookIdleTimeout       = 60 * time.Second
	webhookMaxHeaderBytes    = 16384
	webhookShutdownTimeout   = 10 * time.Second
)

// Sweeper reads one bounded reconciliation pass and commits its continuation
// cursor once the pass's candidates have all been delivered.
type Sweeper interface {
	// Sweep returns the candidates from one bounded pass and the cursor that
	// follows them. It must not record the cursor: CommitSweep does that only
	// after the runtime delivered every candidate.
	Sweep(ctx context.Context) (forge.SweepResult, error)
	// CommitSweep durably records the cursor returned by Sweep.
	CommitSweep(ctx context.Context, cursor string) error
}

// Deliverer accepts one reconciliation candidate. Candidates are delivered in
// the order the sweeper returned them.
type Deliverer func(ctx context.Context, candidate forge.Candidate) error

// HealthEvent names the connector activity behind one health observation.
type HealthEvent string

const (
	// HealthEventSweep is a reconciliation sweep attempt.
	HealthEventSweep HealthEvent = "sweep"
	// HealthEventDelivery is a webhook delivery or sweep delivery attempt.
	HealthEventDelivery HealthEvent = "delivery"
)

// HealthObservation is one connector liveness update.
//
// LastSuccessfulSweep and LastDelivery are the last SUCCESSFUL observations,
// never the current attempt: a failed sweep or delivery must leave the
// timestamp unchanged. Event, Successful and Detail describe the attempt that
// produced this update. QueueDepth is the durable pending-work count per
// operator scope captured at the same instant.
type HealthObservation struct {
	Connector           string
	SweepInterval       time.Duration
	Event               HealthEvent
	Successful          bool
	Detail              string
	LastSuccessfulSweep time.Time
	LastDelivery        time.Time
	Subscription        forge.SubscriptionState
	SubscriptionDetail  string
	QueueDepth          map[string]int
}

// HealthObserver records one liveness update. An error is logged and never
// stops the connector.
type HealthObserver func(ctx context.Context, observation HealthObservation) error

// QueueDepthReader reports the durable pending work-item count per operator
// scope. It is called once per sweep so a health observation carries the queue
// state.
type QueueDepthReader func(ctx context.Context) (map[string]int, error)

// SubscriptionReader reads the forge's event-subscription state. It is
// called once per sweep so a silently disabled subscription becomes observable.
type SubscriptionReader func(ctx context.Context) (forge.SubscriptionState, string)

// Spec is one connector's webhook receiver and reconciliation loop.
type Spec struct {
	// Name identifies the connector in logs and errors.
	Name string
	// SweepInterval is the reconciliation cadence. It must be positive.
	SweepInterval time.Duration
	// Sweeper performs one reconciliation pass.
	Sweeper Sweeper
	// Deliver hands one reconciliation candidate to intake.
	Deliver Deliverer
	// WebhookListen is the bound address. A non-loopback address requires
	// TLSConfig; the runtime validates the address it actually bound.
	WebhookListen string
	// WebhookHandler authenticates and processes deliveries. It must enforce
	// its own request-body bound.
	WebhookHandler http.Handler
	// TLSConfig enables native HTTPS. Nil serves plaintext loopback only.
	TLSConfig *tls.Config
	// Observe receives liveness updates after every sweep and delivery. Nil
	// disables health observation.
	Observe HealthObserver
	// QueueDepth reports the pending work-item count per operator scope. It is
	// called once per sweep; nil reports no queue depth.
	QueueDepth QueueDepthReader
	// Subscription reads the forge subscription state for the observation. Nil
	// reports unknown.
	Subscription SubscriptionReader
}

// Options configures a runtime.
type Options struct {
	// Logger defaults to the standard logger.
	Logger *slog.Logger
	// OnListen reports each bound webhook listener.
	OnListen func(connector string, address net.Addr, tlsEnabled bool)
}

// Runtime runs every connector's webhook receiver and sweep loop.
type Runtime struct {
	specs     []Spec
	logger    *slog.Logger
	onListen  func(string, net.Addr, bool)
	newTicker func(time.Duration) ticker
	health    map[string]*connectorHealth
	now       func() time.Time
}

// connectorHealth is the runtime-owned successful-observation state for one
// connector. Webhook deliveries and sweeps run on separate goroutines, so the
// fields are guarded.
type connectorHealth struct {
	mu                  sync.Mutex
	lastSuccessfulSweep time.Time
	lastDelivery        time.Time
	subscription        forge.SubscriptionState
	subscriptionDetail  string
}

func (h *connectorHealth) status() (time.Time, time.Time, forge.SubscriptionState, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastSuccessfulSweep, h.lastDelivery, h.subscription, h.subscriptionDetail
}

// ticker is the small part of time.Ticker the sweep loop uses, so tests can
// drive scheduling without sleeping.
type ticker interface {
	C() <-chan time.Time
	Stop()
}

type wallTicker struct{ *time.Ticker }

func (t wallTicker) C() <-chan time.Time { return t.Ticker.C }

// New validates the connector runtime before any socket is opened.
func New(specs []Spec, opts Options) (*Runtime, error) {
	if len(specs) == 0 {
		return nil, errors.New("connector runtime: at least one connector is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	seen := make(map[string]struct{}, len(specs))
	copied := make([]Spec, 0, len(specs))
	for _, spec := range specs {
		spec.Name = strings.TrimSpace(spec.Name)
		if spec.Name == "" {
			return nil, errors.New("connector runtime: connector name is required")
		}
		if _, ok := seen[spec.Name]; ok {
			return nil, fmt.Errorf("connector runtime: connector %q is configured twice", spec.Name)
		}
		seen[spec.Name] = struct{}{}
		if spec.SweepInterval <= 0 {
			return nil, fmt.Errorf("connector %q: sweep interval must be positive", spec.Name)
		}
		if spec.Sweeper == nil {
			return nil, fmt.Errorf("connector %q: a sweeper is required", spec.Name)
		}
		if spec.Deliver == nil {
			return nil, fmt.Errorf("connector %q: a candidate deliverer is required", spec.Name)
		}
		if spec.WebhookHandler == nil {
			// A sweep-only connector never binds an inbound listener. Reject a
			// configured address or TLS without a handler instead of silently
			// ignoring them.
			if strings.TrimSpace(spec.WebhookListen) != "" || spec.TLSConfig != nil {
				return nil, fmt.Errorf("connector %q: a webhook listener requires a webhook handler", spec.Name)
			}
		} else {
			spec.WebhookListen = strings.TrimSpace(spec.WebhookListen)
			if spec.WebhookListen == "" {
				return nil, fmt.Errorf("connector %q: a webhook listen address is required", spec.Name)
			}
			if err := httpsecurity.ValidateListen(spec.WebhookListen, spec.TLSConfig != nil); err != nil {
				return nil, fmt.Errorf("connector %q: webhook listener: %w", spec.Name, err)
			}
		}
		copied = append(copied, spec)
	}
	health := make(map[string]*connectorHealth, len(copied))
	for _, spec := range copied {
		health[spec.Name] = &connectorHealth{subscription: forge.SubscriptionUnknown}
	}
	return &Runtime{
		specs:     copied,
		logger:    logger,
		onListen:  opts.OnListen,
		newTicker: func(interval time.Duration) ticker { return wallTicker{time.NewTicker(interval)} },
		health:    health,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

type binding struct {
	spec     Spec
	listener net.Listener
	server   *http.Server
}

// Run binds every webhook listener first, then serves deliveries while each
// connector reconciles on its interval. Cancelling ctx, as SIGINT/SIGTERM does,
// shuts the listeners down gracefully and stops the sweep loops.
func (r *Runtime) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	bindings := make([]binding, 0, len(r.specs))
	closeListeners := func() {
		for _, bound := range bindings {
			_ = bound.listener.Close()
		}
	}
	for _, spec := range r.specs {
		if spec.WebhookHandler == nil {
			continue
		}
		listener, err := net.Listen("tcp", spec.WebhookListen)
		if err != nil {
			closeListeners()
			return fmt.Errorf("connector %q: bind webhook listener %q: %w", spec.Name, spec.WebhookListen, err)
		}
		// Validate what was bound, not only what was requested. This is what
		// refuses a plaintext wildcard bind even if configuration missed it.
		if err := httpsecurity.ValidateListen(listener.Addr().String(), spec.TLSConfig != nil); err != nil {
			_ = listener.Close()
			closeListeners()
			return fmt.Errorf("connector %q: webhook listener: %w", spec.Name, err)
		}
		if spec.TLSConfig != nil {
			listener = tls.NewListener(listener, spec.TLSConfig)
		}
		bindings = append(bindings, binding{
			spec:     spec,
			listener: listener,
			server: &http.Server{
				Handler:           r.observeWebhook(spec, spec.WebhookHandler),
				ReadHeaderTimeout: webhookReadHeaderTimeout,
				ReadTimeout:       webhookReadTimeout,
				WriteTimeout:      webhookWriteTimeout,
				IdleTimeout:       webhookIdleTimeout,
				MaxHeaderBytes:    webhookMaxHeaderBytes,
			},
		})
	}
	defer closeListeners()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	failures := make(chan error, len(bindings))
	var workers sync.WaitGroup
	for _, bound := range bindings {
		bound := bound
		workers.Add(1)
		go func() {
			defer workers.Done()
			err := bound.server.Serve(bound.listener)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				select {
				case failures <- fmt.Errorf("connector %q: webhook listener: %w", bound.spec.Name, err):
				default:
				}
			}
		}()
		if r.onListen != nil {
			r.onListen(bound.spec.Name, bound.listener.Addr(), bound.spec.TLSConfig != nil)
		}
	}
	for _, spec := range r.specs {
		spec := spec
		workers.Add(1)
		go func() {
			defer workers.Done()
			r.runSweep(runCtx, spec)
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-failures:
		cancel()
	}

	shutdownCtx, stop := context.WithTimeout(context.Background(), webhookShutdownTimeout)
	defer stop()
	for _, bound := range bindings {
		if err := bound.server.Shutdown(shutdownCtx); err != nil && runErr == nil {
			runErr = fmt.Errorf("connector %q: shut down webhook listener: %w", bound.spec.Name, err)
		}
	}
	workers.Wait()
	return runErr
}

func (r *Runtime) runSweep(ctx context.Context, spec Spec) {
	ticker := r.newTicker(spec.SweepInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		result, err := spec.Sweeper.Sweep(ctx)
		if err != nil {
			if ctx.Err() == nil {
				r.logger.WarnContext(ctx, "connector reconciliation sweep failed",
					"connector", spec.Name, "error", err)
			}
			r.observe(ctx, spec, HealthEventSweep, false, "reconciliation sweep failed")
		} else {
			r.observe(ctx, spec, HealthEventSweep, true, "")
			delivered := true
			for _, candidate := range result.Items {
				if err := spec.Deliver(ctx, candidate); err != nil {
					delivered = false
					if ctx.Err() == nil {
						r.logger.WarnContext(ctx, "connector intake delivery failed",
							"connector", spec.Name,
							"work_item_id", candidate.WorkItem.ID,
							"revision", candidate.WorkItem.Revision,
							"error", err)
					}
					r.observe(ctx, spec, HealthEventDelivery, false, "intake delivery failed")
				}
				// A sweep delivery is recovery, not a webhook delivery: it
				// must not advance the last-delivery timestamp, or a dead
				// subscription would look healthy while reconciliation works.
			}
			// The watermark stays unadvanced unless every candidate was
			// delivered. The next sweep then replays the batch; the stable
			// workflow identity and the durable intake claim make the replay
			// idempotent.
			if delivered && ctx.Err() == nil {
				if err := spec.Sweeper.CommitSweep(ctx, result.NextCursor); err != nil {
					if ctx.Err() == nil {
						r.logger.WarnContext(ctx, "connector reconciliation watermark not saved",
							"connector", spec.Name, "error", err)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
		}
	}
}

// observeWebhook records the authenticated-delivery outcome after the handler
// has answered. A 401 or 5xx is a failed delivery; a filtered or rejected
// delivery still proves the subscription reached the receiver.
func (r *Runtime) observeWebhook(spec Spec, next http.Handler) http.Handler {
	if spec.Observe == nil {
		return next
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		recorder := &webhookStatusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		successful := recorder.status != http.StatusUnauthorized && recorder.status < http.StatusInternalServerError
		detail := ""
		if !successful {
			detail = "webhook delivery failed with status " + strconv.Itoa(recorder.status)
		}
		r.observe(request.Context(), spec, HealthEventDelivery, successful, detail)
	})
}

type webhookStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *webhookStatusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// observe updates the connector's successful-observation state and calls the
// connector's observer. A nil observer disables observation.
func (r *Runtime) observe(ctx context.Context, spec Spec, event HealthEvent, successful bool, detail string) {
	if spec.Observe == nil {
		return
	}
	tracker := r.health[spec.Name]
	if tracker == nil {
		return
	}
	var subscription forge.SubscriptionState
	var subscriptionDetail string
	var queueDepth map[string]int
	if event == HealthEventSweep {
		subscription = forge.SubscriptionUnknown
		if spec.Subscription != nil {
			state, stateDetail := spec.Subscription(ctx)
			subscription, subscriptionDetail = forge.ParseSubscriptionState(string(state)), stateDetail
		}
		tracker.mu.Lock()
		tracker.subscription, tracker.subscriptionDetail = subscription, subscriptionDetail
		if successful {
			tracker.lastSuccessfulSweep = r.now()
		}
		tracker.mu.Unlock()
		if spec.QueueDepth != nil {
			depths, err := spec.QueueDepth(ctx)
			if err != nil {
				if ctx.Err() == nil {
					r.logger.WarnContext(ctx, "connector queue depth unavailable",
						"connector", spec.Name, "error", err)
				}
			} else {
				queueDepth = depths
			}
		}
	}
	lastSweep, lastDelivery, storedSubscription, storedDetail := tracker.status()
	if event != HealthEventSweep {
		tracker.mu.Lock()
		if successful {
			tracker.lastDelivery = r.now()
		}
		tracker.mu.Unlock()
		lastSweep, lastDelivery, storedSubscription, storedDetail = tracker.status()
	}
	observation := HealthObservation{
		Connector:           spec.Name,
		SweepInterval:       spec.SweepInterval,
		Event:               event,
		Successful:          successful,
		Detail:              detail,
		LastSuccessfulSweep: lastSweep,
		LastDelivery:        lastDelivery,
		Subscription:        storedSubscription,
		SubscriptionDetail:  storedDetail,
		QueueDepth:          queueDepth,
	}
	if err := spec.Observe(ctx, observation); err != nil && ctx.Err() == nil {
		r.logger.WarnContext(ctx, "connector health observation not recorded",
			"connector", spec.Name, "error", err)
	}
}
