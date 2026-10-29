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
	// authMaxTrackedClients bounds the throttle's memory footprint. Once the
	// table is full, new clients share an overflow bucket instead of inserting
	// unbounded state.
	authMaxTrackedClients = 4096
	// authOverflowKey is the shared bucket for clients that arrive while the
	// table is full. It still throttles them; it simply cannot identify them
	// individually.
	authOverflowKey = "__overflow__"
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
	maxTracked  int
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
		maxTracked:  authMaxTrackedClients,
		window:      authFailureWindow,
		cooldown:    authBlockDuration,
	}
}

// allow reports whether the client may attempt authentication, and if not how
// long the caller should wait.
func (t *authThrottle) allow(client string) (bool, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	bucket := t.bucketLocked(client)
	state, ok := t.failures[bucket]
	if !ok {
		return true, 0
	}
	now := t.now()
	if now.Before(state.blockedUntil) {
		return false, state.blockedUntil.Sub(now)
	}
	if !state.first.IsZero() && now.Sub(state.first) > t.window {
		delete(t.failures, bucket)
	}
	return true, 0
}

// fail records one failed authentication attempt.
func (t *authThrottle) fail(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	bucket := t.bucketLocked(client)
	now := t.now()
	state, ok := t.failures[bucket]
	if !ok || (!state.first.IsZero() && now.Sub(state.first) > t.window) {
		state = &authFailure{first: now}
		t.failures[bucket] = state
	}
	state.count++
	if state.count >= t.maxFailures {
		state.blockedUntil = now.Add(t.cooldown)
	}
}

// reset clears a client's failure state after a successful authentication.
// A client that was aggregated into the shared overflow bucket also clears
// that bucket: whoever proved possession of the token is at least as trusted
// as the anonymous attempts it was aggregated with.
func (t *authThrottle) reset(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, client)
	delete(t.failures, authOverflowKey)
}

// bucketLocked maps a client to the bucket that tracks it. Existing clients
// keep their own bucket; once the table is full, new clients are aggregated
// into a shared overflow bucket so the table cannot grow without bound.
func (t *authThrottle) bucketLocked(client string) string {
	if _, ok := t.failures[client]; ok {
		return client
	}
	if len(t.failures) < t.maxTracked {
		return client
	}
	t.pruneLocked()
	if len(t.failures) < t.maxTracked {
		return client
	}
	return authOverflowKey
}

func (t *authThrottle) pruneLocked() {
	now := t.now()
	for client, state := range t.failures {
		if now.Sub(state.first) > t.window && !now.Before(state.blockedUntil) {
			delete(t.failures, client)
		}
	}
}

const maxClientKeyLength = 64

// clientKey derives a bounded throttle key.
//
// X-Forwarded-For is honoured only when the TCP peer is loopback, i.e. when a
// local reverse proxy (the documented nginx/Tailscale shapes) is the one
// talking to the console. A direct or non-loopback client cannot rotate the
// key by forging the header; it is keyed by its TCP peer address. The key is
// length-bounded so a long header cannot grow the throttle's memory.
func clientKey(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || host == "" {
		host = request.RemoteAddr
	}
	if host == "" {
		return "unknown"
	}
	if isLoopbackHost(host) {
		if forwarded := request.Header.Get("X-Forwarded-For"); forwarded != "" {
			first, _, _ := strings.Cut(forwarded, ",")
			if key := strings.TrimSpace(first); key != "" {
				return truncateClientKey(key)
			}
		}
	}
	return truncateClientKey(host)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func truncateClientKey(key string) string {
	if len(key) > maxClientKeyLength {
		return key[:maxClientKeyLength]
	}
	return key
}
