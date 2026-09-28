// Package notify delivers bounded security alerts from the factory to an
// operator-configured sink.
//
// Alerting is strictly best-effort. The factory must never fail a run because
// an alert could not be delivered, so delivery is bounded in both wall-clock
// time and payload size, failures are returned to the caller for logging, and
// nothing here retries, blocks, or panics on a misbehaving endpoint.
//
// All payload-bound decisions are made on the serialized JSON contribution of
// a field, not on the raw Go string length, so the bound holds on the wire even
// for strings containing characters that JSON escapes.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Severity classifies an alert for the operator.
type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityWarning  Severity = "WARNING"
	SeverityCritical Severity = "CRITICAL"
)

// Event is one bounded alert. Detail values are already redacted by the caller.
type Event struct {
	Kind     string            `json:"kind"`
	Severity Severity          `json:"severity"`
	RunID    string            `json:"run_id,omitempty"`
	Summary  string            `json:"summary"`
	Detail   map[string]string `json:"detail,omitempty"`
	At       time.Time         `json:"at"`
}

// Notifier delivers one event. Implementations must be safe for concurrent use.
type Notifier interface {
	Notify(ctx context.Context, e Event) error
}

// Multi fans out to every notifier, attempting all of them, and returns an
// error that joins every failure (nil if all succeed).
type Multi []Notifier

// Notify calls every non-nil notifier in order, even after earlier ones fail.
// Failures are wrapped with the notifier's index and joined with errors.Join,
// so errors.Is/errors.As still reach the original errors. A nil element is
// skipped rather than dereferenced.
func (m Multi) Notify(ctx context.Context, e Event) error {
	errs := make([]error, 0, len(m))
	for i, n := range m {
		if n == nil {
			continue
		}
		if err := n.Notify(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("notify: notifier %d: %w", i, err))
		}
	}
	return errors.Join(errs...)
}

// Nop discards events. It is the default when no webhook is configured.
type Nop struct{}

// Notify implements Notifier and always succeeds.
func (Nop) Notify(context.Context, Event) error { return nil }

// WebhookConfig configures an HTTP webhook notifier.
type WebhookConfig struct {
	// URL is the endpoint. Must be http or https.
	URL string
	// Token, when set, is sent as "Authorization: Bearer <token>".
	Token string
	// Timeout bounds one delivery attempt. Defaults to 5s.
	Timeout time.Duration
	// MaxDetailBytes bounds the total size of Detail values. Defaults to 2048.
	MaxDetailBytes int
	// Client allows tests to inject an *http.Client. Defaults to a client with Timeout.
	Client *http.Client
}

const (
	defaultTimeout        = 5 * time.Second
	defaultMaxDetailBytes = 2048
	maxSummaryBytes       = 512
	maxErrBodyBytes       = 512
	truncationSuffix      = "..."
)

// Webhook posts events as JSON to a single operator-configured HTTP endpoint.
//
// A Webhook is immutable after construction and therefore safe for concurrent
// use by multiple goroutines.
type Webhook struct {
	url            *url.URL
	token          string
	timeout        time.Duration
	maxDetailBytes int
	client         *http.Client
}

// NewWebhook validates cfg and returns a notifier.
//
// It rejects URLs that do not parse, that are not http/https, that have no
// host, or that carry userinfo (user:pass@), which would leak a credential into
// logs and proxy access logs. A configured Token is the only supported way to
// authenticate.
func NewWebhook(cfg WebhookConfig) (*Webhook, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("notify: invalid webhook URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("notify: webhook URL scheme %q is not http or https", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("notify: webhook URL must include a host")
	}
	if u.User != nil {
		return nil, errors.New("notify: webhook URL must not contain userinfo (user:pass@); use WebhookConfig.Token instead")
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxDetail := cfg.MaxDetailBytes
	if maxDetail <= 0 {
		maxDetail = defaultMaxDetailBytes
	}

	// Never mutate a caller-supplied client (it may be shared); copy it. The
	// Transport, if any, is deliberately shared: http.Transport is itself safe
	// for concurrent use and is what makes connection pooling work.
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	} else {
		cloned := *client
		client = &cloned
	}
	// Redirect policy is a credential-safety invariant of this notifier, not a
	// caller preference, so it is installed on our (possibly cloned) client
	// even when the caller supplied its own CheckRedirect.
	client.CheckRedirect = rejectCrossHostRedirect(u)

	return &Webhook{
		url:            u,
		token:          cfg.Token,
		timeout:        timeout,
		maxDetailBytes: maxDetail,
		client:         client,
	}, nil
}

// Notify delivers e to the webhook. The event is bounded before serialization,
// the request honours ctx cancellation and deadline (and the configured
// timeout, whichever expires first), and any 2xx response is success.
//
// The returned error is intended for logging by a caller that must not fail a
// run because of it; it never contains the bearer token.
func (w *Webhook) Notify(ctx context.Context, e Event) error {
	if w == nil {
		return errors.New("notify: nil webhook")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	body, err := json.Marshal(w.bound(e))
	if err != nil {
		// Event only contains plain strings, a string map and a time.Time, so
		// this is unreachable in practice; return it rather than panicking.
		return fmt.Errorf("notify: encode event: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if w.token != "" {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: deliver to %s: %w", w.url.Host, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Drain a bounded amount so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}

	snippet := readBounded(resp.Body, maxErrBodyBytes)
	if w.token != "" {
		// A misconfigured or malicious receiver can echo Authorization in an
		// error body. That body is logged by the caller, so remove the token.
		snippet = strings.ReplaceAll(snippet, w.token, "[REDACTED]")
	}
	if snippet == "" {
		return fmt.Errorf("notify: webhook returned status %d", resp.StatusCode)
	}
	return fmt.Errorf("notify: webhook returned status %d: %s", resp.StatusCode, snippet)
}

// bound returns a copy of e whose Summary and Detail fit their configured
// bounds. e is not mutated.
func (w *Webhook) bound(e Event) Event {
	e.Summary = truncate(sanitize(e.Summary), maxSummaryBytes)
	if len(e.Detail) > 0 {
		e.Detail = boundDetail(e.Detail, w.maxDetailBytes)
	}
	return e
}

// rejectCrossHostRedirect returns a CheckRedirect policy that follows a
// redirect only when it stays on the host (and port) the operator configured.
//
// The request carries the operator's bearer token. Go's http.Client strips
// Authorization on a cross-host redirect, but a compromised or misconfigured
// endpoint could still bounce the alert body to an attacker-controlled host,
// and any future credential added to this request would follow it. Refusing
// outright pins the token and the alert to the configured host. An https -> http
// redirect is refused for the same reason: it would put the token on the wire
// in cleartext even though the host is unchanged.
func rejectCrossHostRedirect(base *url.URL) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, _ []*http.Request) error {
		if !sameHost(base, req.URL) {
			return fmt.Errorf("notify: refusing redirect to a different host %q", req.URL.Host)
		}
		if strings.EqualFold(base.Scheme, "https") && strings.EqualFold(req.URL.Scheme, "http") {
			return fmt.Errorf("notify: refusing https -> http redirect to %q", req.URL.Host)
		}
		return nil
	}
}

// sameHost reports whether a and b address the same host and effective port
// (default ports are normalized, so https://h and https://h:443 match).
func sameHost(a, b *url.URL) bool {
	if !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// boundDetail returns detail values whose combined serialized contribution to
// the event object does not exceed max bytes (including the object's braces and
// separators).
//
// The budget is allocated greedily over keys sorted lexicographically, which
// makes truncation deterministic across runs: each value is shortened with an
// ellipsis when it does not fit, and a key with no room at all is dropped. The
// returned JSON object is always valid, because every value is measured and cut
// with the same encoding/json escaping used to serialize the event.
func boundDetail(detail map[string]string, max int) map[string]string {
	if max <= 0 || len(detail) == 0 {
		return nil
	}
	rawKeys := make([]string, 0, len(detail))
	for k := range detail {
		rawKeys = append(rawKeys, k)
	}
	sort.Strings(rawKeys)

	out := make(map[string]string, len(rawKeys))
	seen := make(map[string]bool, len(rawKeys))
	remaining := max - len("{}")
	first := true
	for _, rawKey := range rawKeys {
		key := sanitize(rawKey)
		if seen[key] {
			// Two distinct keys collapsed onto the same sanitized key; keep the
			// first (deterministic) one.
			continue
		}
		keyCost := quotedLen(key) + len(":")
		sep := 0
		if !first {
			sep = len(",")
		}
		// A value needs at least its two quotes.
		if remaining < keyCost+len(`""`)+sep {
			continue
		}
		budget := remaining - keyCost - sep // allowed quoted length of the value
		value, _ := truncateQuoted(sanitize(detail[rawKey]), budget)
		out[key] = value
		remaining -= keyCost + quotedLen(value) + sep
		seen[key] = true
		first = false
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncate bounds s to at most max bytes, appending an ellipsis when it cuts,
// and trims the cut back to a rune boundary so the result stays valid UTF-8.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= len(truncationSuffix) {
		return truncationSuffix[:max]
	}
	cut := max - len(truncationSuffix)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + truncationSuffix
}

// truncateQuoted shortens s so that its JSON-quoted form is at most budget
// bytes, appending an ellipsis when anything was removed. It reports whether a
// truncation happened.
func truncateQuoted(s string, budget int) (string, bool) {
	if quotedLen(s) <= budget {
		return s, false
	}
	suffixLen := quotedLen(truncationSuffix)
	if budget < suffixLen {
		return "", true
	}
	target := budget - suffixLen

	// JSON escaping never shrinks content, so a prefix longer than target can
	// never fit; cutting the input first keeps the search below tiny even for
	// multi-megabyte values.
	if len(s) > target {
		cut := target
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	runes := []rune(s)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if quotedLen(string(runes[:mid])) <= target {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:lo]) + truncationSuffix, true
}

// quotedLen is the serialized byte length of s as a JSON string, including its
// quotes and any escaping.
func quotedLen(s string) int {
	b, err := json.Marshal(s)
	if err != nil {
		return len(s) + len(`""`)
	}
	return len(b)
}

// sanitize replaces invalid UTF-8 with the Unicode replacement character, which
// is exactly what encoding/json would do, so measured lengths match wire bytes.
func sanitize(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}

// readBounded reads at most max bytes from r, returning valid UTF-8 and marking
// truncation with an ellipsis. The response body is never echoed unbounded.
func readBounded(r io.Reader, max int) string {
	b, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil && len(b) == 0 {
		return ""
	}
	text := strings.ToValidUTF8(string(b), "\uFFFD")
	if len(b) > max {
		text = strings.ToValidUTF8(string(b[:max]), "\uFFFD") + truncationSuffix
	}
	return text
}
