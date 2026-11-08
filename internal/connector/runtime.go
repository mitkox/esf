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

// Sweeper reads one bounded reconciliation pass.
type Sweeper interface {
	Sweep(ctx context.Context) ([]forge.Candidate, error)
}

// Deliverer accepts one reconciliation candidate. Candidates are delivered in
// the order the sweeper returned them.
type Deliverer func(ctx context.Context, candidate forge.Candidate) error

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
			return nil, fmt.Errorf("connector %q: a webhook handler is required", spec.Name)
		}
		spec.WebhookListen = strings.TrimSpace(spec.WebhookListen)
		if spec.WebhookListen == "" {
			return nil, fmt.Errorf("connector %q: a webhook listen address is required", spec.Name)
		}
		if err := httpsecurity.ValidateListen(spec.WebhookListen, spec.TLSConfig != nil); err != nil {
			return nil, fmt.Errorf("connector %q: webhook listener: %w", spec.Name, err)
		}
		copied = append(copied, spec)
	}
	return &Runtime{
		specs:     copied,
		logger:    logger,
		onListen:  opts.OnListen,
		newTicker: func(interval time.Duration) ticker { return wallTicker{time.NewTicker(interval)} },
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
				Handler:           spec.WebhookHandler,
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
		candidates, err := spec.Sweeper.Sweep(ctx)
		if err != nil {
			if ctx.Err() == nil {
				r.logger.WarnContext(ctx, "connector reconciliation sweep failed",
					"connector", spec.Name, "error", err)
			}
		} else {
			for _, candidate := range candidates {
				if err := spec.Deliver(ctx, candidate); err != nil && ctx.Err() == nil {
					r.logger.WarnContext(ctx, "connector intake delivery failed",
						"connector", spec.Name,
						"work_item_id", candidate.WorkItem.ID,
						"revision", candidate.WorkItem.Revision,
						"error", err)
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
