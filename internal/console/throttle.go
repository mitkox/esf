package console

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// authMaxFailures bounds failed bearer attempts per client inside one
	// observation window before the client is temporarily blocked.
	authMaxFailures = 10
	// authFailureWindow is how long failures are accumulated.
	authFailureWindow = 5 * time.Minute
	// authBlockDuration is the temporary lockout after too many failures.
	authBlockDuration = time.Minute
	// authMaxTrackedClients bounds the throttle's memory footprint.
	authMaxTrackedClients = 4096
)

// authThrottle is a small, dependency-free per-client failure limiter in front
// of bearer authentication. It exists because the console can be exposed
// through a TLS reverse proxy (including a public tailnet funnel): without it,
// an unauthenticated attacker can try tokens indefinitely.
type authThrottle struct {
	mu          sync.Mutex
	failures    map[string]*authFailure
	now         func() time.Time
	maxFailures int
	window      time.Duration
	cooldown    time.Duration
}

type authFailure struct {
	count        int
	first        time.Time
	blockedUntil time.Time
}

func newAuthThrottle(now func() time.Time) *authThrottle {
	if now == nil {
		now = time.Now
	}
	return &authThrottle{
		failures:    map[string]*authFailure{},
		now:         now,
		maxFailures: authMaxFailures,
		window:      authFailureWindow,
		cooldown:    authBlockDuration,
	}
}

// allow reports whether the client may attempt authentication, and if not how
// long the caller should wait.
func (t *authThrottle) allow(client string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked()
	state, ok := t.failures[client]
	if !ok {
		return true, 0
	}
	now := t.now()
	if now.Before(state.blockedUntil) {
		return false, state.blockedUntil.Sub(now)
	}
	if !state.first.IsZero() && now.Sub(state.first) > t.window {
		delete(t.failures, client)
	}
	return true, 0
}

// fail records one failed authentication attempt.
func (t *authThrottle) fail(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	state, ok := t.failures[client]
	if !ok || (!state.first.IsZero() && now.Sub(state.first) > t.window) {
		state = &authFailure{first: now}
		t.failures[client] = state
	}
	state.count++
	if state.count >= t.maxFailures {
		state.blockedUntil = now.Add(t.cooldown)
	}
}

// reset clears a client's failure state after a successful authentication.
func (t *authThrottle) reset(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, client)
}

func (t *authThrottle) pruneLocked() {
	if len(t.failures) < authMaxTrackedClients {
		return
	}
	now := t.now()
	for client, state := range t.failures {
		if now.After(state.blockedUntil) && now.Sub(state.first) > t.window {
			delete(t.failures, client)
		}
	}
}

const maxClientKeyLength = 64

// clientKey derives a bounded throttle key. A trusted reverse proxy (the
// documented deployment) supplies X-Forwarded-For; otherwise the TCP peer
// address is used. The header is only a grouping hint, never an authorization
// input, and is bounded so it cannot grow the throttle's memory.
func clientKey(request *http.Request) string {
	if forwarded := request.Header.Get("X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if key := strings.TrimSpace(first); key != "" {
			return truncateClientKey(key)
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && host != "" {
		return truncateClientKey(host)
	}
	if request.RemoteAddr != "" {
		return truncateClientKey(request.RemoteAddr)
	}
	return "unknown"
}

func truncateClientKey(key string) string {
	if len(key) > maxClientKeyLength {
		return key[:maxClientKeyLength]
	}
	return key
}
