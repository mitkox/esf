package console

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
)

// liveService owns the console's bounded, read-only views of the live system:
// workflows currently executing in Temporal and sandboxes currently present in
// CubeSandbox.
//
// Both are optional. When either backend is unreachable, the console reports
// "unavailable" with the reason instead of showing stale or invented state.
// Every call is bounded by a context timeout and cached so a dashboard refresh
// cannot stampede production infrastructure.
type liveService struct {
	cfg  factory.Config
	live bool
	now  func() time.Time

	mu        sync.Mutex
	client    temporalclient.Client
	clientErr error
	clientAt  time.Time

	snapshot   liveSnapshot
	snapshotAt time.Time
	runRefresh chan struct{}

	provider       *cube.Provider
	providerErr    error
	providerAt     time.Time
	sandboxCache   sandboxSnapshot
	sandboxAt      time.Time
	sandboxRefresh chan struct{}
}

type liveSnapshot struct {
	Available   bool
	Error       string
	CheckedAt   time.Time
	Runs        []LiveRun
	UnknownRuns int
}

type sandboxSnapshot struct {
	Available bool
	Error     string
	CheckedAt time.Time
	Sandboxes []sandbox.Info
}

const (
	liveCacheAge      = 5 * time.Second
	sandboxCacheAge   = 20 * time.Second
	clientRetryAge    = 20 * time.Second
	liveCallTimeout   = 6 * time.Second
	statusCallTimeout = 3 * time.Second
	maxStatusQueries  = 40
)

func newLiveService(cfg factory.Config, enabled bool, now func() time.Time) *liveService {
	if now == nil {
		now = time.Now
	}
	return &liveService{cfg: cfg, live: enabled, now: now, runRefresh: make(chan struct{}, 1), sandboxRefresh: make(chan struct{}, 1)}
}

func (l *liveService) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != nil {
		l.client.Close()
		l.client = nil
	}
}

func (l *liveService) temporalClient(ctx context.Context) (temporalclient.Client, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != nil {
		return l.client, nil
	}
	if l.clientErr != nil && l.now().Sub(l.clientAt) < clientRetryAge {
		return nil, l.clientErr
	}
	if strings.TrimSpace(l.cfg.Temporal.HostPort) == "" {
		l.clientErr = errNoTemporalConfig
		l.clientAt = l.now()
		return nil, l.clientErr
	}
	dialCtx, cancel := context.WithTimeout(ctx, liveCallTimeout)
	defer cancel()
	type dialResult struct {
		client temporalclient.Client
		err    error
	}
	// Unbuffered: if the caller times out first, the dial goroutine cannot
	// park a connected client in a buffered channel; it closes it instead.
	results := make(chan dialResult)
	go func() {
		client, err := factory.NewTemporalClient(l.cfg, nil)
		select {
		case results <- dialResult{client: client, err: err}:
		case <-dialCtx.Done():
			if client != nil {
				client.Close()
			}
		}
	}()
	select {
	case result := <-results:
		if result.err != nil {
			l.clientErr = result.err
			l.clientAt = l.now()
			return nil, result.err
		}
		l.client = result.client
		l.clientErr = nil
		return result.client, nil
	case <-dialCtx.Done():
		l.clientErr = &consoleError{"Temporal did not accept a connection before the console timeout."}
		l.clientAt = l.now()
		return nil, l.clientErr
	}
}

var errNoTemporalConfig = &consoleError{"Temporal is not configured on this factory host."}

type consoleError struct{ message string }

func (e *consoleError) Error() string { return e.message }

// openRuns returns the workflows currently executing, enriched with the
// workflow's own status query where that is reachable.
func (l *liveService) openRuns(ctx context.Context) liveSnapshot {
	if !l.live {
		return liveSnapshot{Error: "Live orchestration is disabled."}
	}
	select {
	case l.runRefresh <- struct{}{}:
		defer func() { <-l.runRefresh }()
	case <-ctx.Done():
		return liveSnapshot{Error: friendlyTemporalError(ctx.Err())}
	}
	l.mu.Lock()
	if !l.snapshotAt.IsZero() && l.now().Sub(l.snapshotAt) < liveCacheAge {
		snapshot := l.snapshot
		l.mu.Unlock()
		return snapshot
	}
	l.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, liveCallTimeout)
	defer cancel()
	client, err := l.temporalClient(callCtx)
	if err != nil {
		return l.storeSnapshot(liveSnapshot{Error: friendlyTemporalError(err)})
	}
	converter, err := factory.TemporalDataConverter(l.cfg)
	if err != nil {
		return l.storeSnapshot(liveSnapshot{Error: "Temporal payload converter is misconfigured: " + err.Error()})
	}
	open, err := factory.ListOpenRuns(callCtx, client, factory.HaltFilter{}, converter)
	if err != nil {
		return l.storeSnapshot(liveSnapshot{Error: friendlyTemporalError(err)})
	}
	// Enriching one workflow is a query; enriching forty is a stalled request.
	// Bound the whole enrichment pass, not just each call, and degrade the rest
	// to the list-view state.
	enrichCtx, enrichCancel := context.WithTimeout(ctx, liveCallTimeout)
	defer enrichCancel()
	runs := make([]LiveRun, 0, len(open))
	for index, item := range open {
		live := LiveRun{
			RunID:      item.RunID,
			WorkflowID: item.WorkflowID,
			Harness:    item.Harness,
			Scope:      item.Scope,
			Repository: item.Repository,
			StartedAt:  item.StartedAt.UTC(),
			State:      "UNKNOWN",
		}
		if index < maxStatusQueries && enrichCtx.Err() == nil {
			statusCtx, statusCancel := context.WithTimeout(enrichCtx, statusCallTimeout)
			status, statusErr := factory.QueryRunStatus(statusCtx, client, item.WorkflowID, "")
			statusCancel()
			if statusErr != nil {
				live.QueryError = friendlyTemporalError(statusErr)
			} else {
				live.State = string(status.State)
				live.CurrentStep = status.CurrentStep
				live.ChangeID = status.ChangeID
				live.SandboxID = status.SandboxID
				live.SandboxDestroyed = status.SandboxGone
				live.Conditions = status.Conditions
				if live.Scope == "" {
					live.Scope = status.Scope
				}
			}
		} else if index >= maxStatusQueries {
			live.QueryError = "status query skipped: too many open runs in this refresh"
		} else {
			live.QueryError = "status query skipped: console refresh deadline reached"
		}
		runs = append(runs, live)
	}
	sort.Slice(runs, func(a, b int) bool { return runs[a].StartedAt.Before(runs[b].StartedAt) })
	unknown := 0
	for _, run := range runs {
		if run.QueryError != "" {
			unknown++
		}
	}
	return l.storeSnapshot(liveSnapshot{Available: true, CheckedAt: l.now().UTC(), Runs: runs, UnknownRuns: unknown})
}

func (l *liveService) storeSnapshot(snapshot liveSnapshot) liveSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.snapshot = snapshot
	l.snapshotAt = l.now()
	return snapshot
}

// runStatus queries one workflow. It is used when a caller asks for a run that
// has no durable manifest yet, which is precisely the in-flight case.
func (l *liveService) runStatus(ctx context.Context, runID string) (*factory.RunStatus, error) {
	if !l.live {
		return nil, &consoleError{"Live orchestration is disabled."}
	}
	callCtx, cancel := context.WithTimeout(ctx, liveCallTimeout)
	defer cancel()
	client, err := l.temporalClient(callCtx)
	if err != nil {
		return nil, err
	}
	statusCtx, statusCancel := context.WithTimeout(ctx, statusCallTimeout)
	defer statusCancel()
	status, err := factory.QueryRunStatus(statusCtx, client, factory.WorkflowIDForRun(runID), "")
	if err != nil {
		return nil, err
	}
	return &status, nil
}

// sandboxes returns the live CubeSandbox inventory.
func (l *liveService) sandboxes(ctx context.Context) sandboxSnapshot {
	if !l.live {
		return sandboxSnapshot{Error: "Live sandbox inventory is disabled."}
	}
	select {
	case l.sandboxRefresh <- struct{}{}:
		defer func() { <-l.sandboxRefresh }()
	case <-ctx.Done():
		return sandboxSnapshot{Error: ctx.Err().Error()}
	}
	l.mu.Lock()
	if !l.sandboxAt.IsZero() && l.now().Sub(l.sandboxAt) < sandboxCacheAge {
		snapshot := l.sandboxCache
		l.mu.Unlock()
		return snapshot
	}
	if l.provider == nil {
		if l.providerErr != nil && l.now().Sub(l.providerAt) < clientRetryAge {
			err := l.providerErr
			l.mu.Unlock()
			return sandboxSnapshot{Error: err.Error()}
		}
		provider, err := cube.New(l.cfg.Cube)
		if err != nil {
			l.providerErr = err
			l.providerAt = l.now()
			l.mu.Unlock()
			return sandboxSnapshot{Error: "CubeSandbox is not configured correctly: " + err.Error()}
		}
		l.provider = provider
		l.providerErr = nil
	}
	provider := l.provider
	l.mu.Unlock()

	callCtx, cancel := context.WithTimeout(ctx, liveCallTimeout)
	defer cancel()
	infos, err := provider.List(callCtx)
	if err != nil {
		return l.storeSandboxes(sandboxSnapshot{Error: "CubeSandbox inventory unavailable: " + err.Error()})
	}
	return l.storeSandboxes(sandboxSnapshot{Available: true, CheckedAt: l.now().UTC(), Sandboxes: infos})
}

func (l *liveService) storeSandboxes(snapshot sandboxSnapshot) sandboxSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sandboxCache = snapshot
	l.sandboxAt = l.now()
	return snapshot
}

func friendlyTemporalError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if strings.Contains(message, "context deadline exceeded") {
		return "Temporal did not answer before the console timeout."
	}
	return message
}
