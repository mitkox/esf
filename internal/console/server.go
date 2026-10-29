package console

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/httpsecurity"
)

// Config configures one factory console server.
type Config struct {
	// Factory is the loaded operator configuration. It is used for the catalog,
	// posture and optional live probes; the console never mutates factory state.
	Factory factory.Config
	// ConfigPath is recorded in the catalog for operator orientation.
	ConfigPath string
	// DataDir is the resolved artifact store root (factory storage.data_dir).
	DataDir string
	// ReadToken is the bearer credential required for API access. Empty is only
	// allowed together with AllowNoAuth on a loopback listener.
	ReadToken string
	// AllowNoAuth permits an empty read token. The command refuses this on a
	// non-loopback listener.
	AllowNoAuth bool
	// Environment is shown in the UI, for example "production", "staging",
	// "development" or "self-hosted".
	Environment string
	// Listen is the address the server will bind. It is used for the no-auth
	// safety check and shown in the UI.
	Listen string
	// Live enables bounded Temporal and CubeSandbox read-only integration.
	Live bool
	// TLSCertFile and TLSKeyFile enable native HTTPS. Both are required
	// together. When empty the console serves loopback HTTP for a terminating
	// reverse proxy or local evaluation.
	TLSCertFile string
	TLSKeyFile  string
	// RefreshInterval bounds how often the evidence index is rebuilt.
	RefreshInterval time.Duration
	// ThreatLimit caps per-run entries in the cross-run security view.
	ThreatLimit int
	// Version is the factory implementation version shown in the UI.
	Version string
	// Logger receives diagnostics. Defaults to a discard logger.
	Logger *slog.Logger
	// Now is a clock seam for tests.
	Now func() time.Time
	// Static overrides the embedded SPA filesystem in tests.
	Static fs.FS
	// ProbeDialer overrides the reachability dialer in tests.
	ProbeDialer func(ctx context.Context, network, address string) (net.Conn, error)
}

// Server is the read-only factory console.
type Server struct {
	cfg     Config
	root    string
	index   *evidenceIndex
	live    *liveService
	token   []byte
	static  http.Handler
	handler http.Handler
	now     func() time.Time
	start   time.Time
	log     *slog.Logger

	probeMu  sync.Mutex
	probes   []Probe
	probesAt time.Time

	tlsConfig  *tls.Config
	tlsEnabled bool
	throttle   *authThrottle

	configWarnings []string
}

// New validates the configuration and builds a console server.
func New(cfg Config) (*Server, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("console: data directory is required")
	}
	root, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("console: resolve data directory: %w", err)
	}
	token := strings.TrimSpace(cfg.ReadToken)
	if token == "" && !cfg.AllowNoAuth {
		return nil, errors.New("console: a read token is required (or --no-auth on a loopback listener for local evaluation)")
	}
	if token != "" && len(token) < 32 {
		return nil, errors.New("console: the read token must be at least 32 characters")
	}
	if token != "" && !httpsecurity.ValidBearerToken(token) {
		return nil, errors.New("console: the read token must use the ASCII bearer token alphabet without whitespace")
	}
	if cfg.Environment == "" {
		cfg.Environment = "self-hosted"
	}
	if cfg.Version == "" {
		cfg.Version = factory.Version
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 15 * time.Second
	}
	if cfg.ThreatLimit <= 0 {
		cfg.ThreatLimit = 100
	}

	tlsConfig, err := loadTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, err
	}

	static, err := newStaticHandler(cfg.Static)
	if err != nil {
		return nil, err
	}

	server := &Server{
		cfg:        cfg,
		root:       root,
		index:      newEvidenceIndex(root, cfg.RefreshInterval, cfg.ThreatLimit, cfg.Now),
		live:       newLiveService(cfg.Factory, cfg.Live, cfg.Now),
		token:      []byte(token),
		static:     static,
		now:        cfg.Now,
		start:      cfg.Now(),
		log:        cfg.Logger,
		tlsConfig:  tlsConfig,
		tlsEnabled: tlsConfig != nil,
		throttle:   newAuthThrottle(cfg.Now),
	}
	// Validate the operator configuration once at startup. This is a policy
	// review, not an execution path: the console reports what it finds and
	// never refuses to show evidence because the policy needs attention.
	if err := cfg.Factory.Validate(); err != nil {
		server.configWarnings = append(server.configWarnings, "Configuration validation: "+err.Error())
	}
	// Prime the index so the first request does not pay the scan cost. A
	// failure is not fatal: the console must still start to show that evidence
	// is missing, which is itself an operational finding.
	if err := server.index.refresh(true); err != nil {
		server.log.Error("console: initial evidence scan failed", slog.Any("error", err))
	}
	server.handler = server.routes()
	return server, nil
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Close releases the Temporal client held by the live service.
func (s *Server) Close() { s.live.close() }

// RoomForAuth reports whether bearer authentication is enforced.
func (s *Server) authRequired() bool { return len(s.token) > 0 }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/console/healthz", s.handleHealthz)
	mux.Handle("GET /api/v1/console/session", s.requireAuth(http.HandlerFunc(s.handleSession)))
	mux.Handle("GET /api/v1/console/overview", s.requireAuth(http.HandlerFunc(s.handleOverview)))
	mux.Handle("GET /api/v1/console/runs", s.requireAuth(http.HandlerFunc(s.handleRuns)))
	mux.Handle("GET /api/v1/console/runs/{id}", s.requireAuth(http.HandlerFunc(s.handleRunDetail)))
	mux.Handle("GET /api/v1/console/runs/{id}/patch", s.requireAuth(http.HandlerFunc(s.handleRunPatch)))
	mux.Handle("GET /api/v1/console/runs/{id}/artifacts", s.requireAuth(http.HandlerFunc(s.handleRunArtifact)))
	mux.Handle("GET /api/v1/console/changes", s.requireAuth(http.HandlerFunc(s.handleChanges)))
	mux.Handle("GET /api/v1/console/changes/{id}", s.requireAuth(http.HandlerFunc(s.handleChangeDetail)))
	mux.Handle("GET /api/v1/console/reviews", s.requireAuth(http.HandlerFunc(s.handleReviews)))
	mux.Handle("GET /api/v1/console/security", s.requireAuth(http.HandlerFunc(s.handleSecurity)))
	mux.Handle("GET /api/v1/console/operations", s.requireAuth(http.HandlerFunc(s.handleOperations)))
	mux.Handle("GET /api/v1/console/catalog", s.requireAuth(http.HandlerFunc(s.handleCatalog)))
	mux.Handle("GET /api/v1/console/search", s.requireAuth(http.HandlerFunc(s.handleSearch)))
	// Unknown API paths must be JSON 404s, not the SPA shell.
	mux.HandleFunc("/api/", func(response http.ResponseWriter, request *http.Request) {
		writeError(response, http.StatusNotFound, "not_found", "No such console endpoint.")
	})
	mux.Handle("/", s.static)
	return s.securityHeaders(s.recoverer(s.accessLog(s.compression(mux))))
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !s.authRequired() {
			next.ServeHTTP(response, request)
			return
		}
		key := clientKey(request)
		if allowed, retryAfter := s.throttle.allow(key); !allowed {
			seconds := int((retryAfter + time.Second - 1) / time.Second)
			response.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(response, http.StatusTooManyRequests, "rate_limited", "Too many failed authentication attempts. Try again later.")
			return
		}
		if !httpsecurity.Authenticated(request, string(s.token)) {
			s.throttle.fail(key)
			response.Header().Set("WWW-Authenticate", `Bearer realm="ESF factory console"`)
			writeError(response, http.StatusUnauthorized, "unauthorized", "A valid console access token is required.")
			return
		}
		s.throttle.reset(key)
		next.ServeHTTP(response, request)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if s.tlsEnabled {
			// The console is TLS-only when configured with a certificate; tell
			// browsers never to downgrade. The value is deliberately one year
			// without includeSubDomains so a host deployment decides its scope.
			response.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if strings.HasPrefix(request.URL.Path, "/api/") {
			response.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("console: handler panic", slog.Any("panic", recovered), slog.String("path", request.URL.Path))
				writeError(response, http.StatusInternalServerError, "internal", "The console hit an unexpected error.")
			}
		}()
		next.ServeHTTP(response, request)
	})
}

// compressible is the small allowlist of response types worth compressing.
// Already-compressed formats (woff2, images) are deliberately absent: gzipping
// them spends CPU to make the response larger.
func compressible(contentType string) bool {
	contentType = strings.ToLower(contentType)
	switch {
	case strings.HasPrefix(contentType, "text/"):
		return true
	case strings.HasPrefix(contentType, "application/json"):
		return true
	case strings.HasPrefix(contentType, "application/javascript"):
		return true
	case strings.HasPrefix(contentType, "application/xml"):
		return true
	case strings.HasPrefix(contentType, "image/svg+xml"):
		return true
	default:
		return false
	}
}

var gzipPool = sync.Pool{New: func() any {
	writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	return writer
}}

// compression gzips compressible responses when the client advertises support.
// The SPA bundle and the JSON API are both large enough that this materially
// improves first load on a remote operator workstation.
func (s *Server) compression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodHead || !strings.Contains(request.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(response, request)
			return
		}
		response.Header().Add("Vary", "Accept-Encoding")
		writer := &gzipResponseWriter{ResponseWriter: response}
		defer writer.Close()
		next.ServeHTTP(writer, request)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer      *gzip.Writer
	decided     bool
	compressed  bool
	wroteHeader bool
}

func (g *gzipResponseWriter) decide() {
	if g.decided {
		return
	}
	g.decided = true
	if compressible(g.Header().Get("Content-Type")) {
		g.writer = gzipPool.Get().(*gzip.Writer)
		g.writer.Reset(g.ResponseWriter)
		g.compressed = true
	}
}

func (g *gzipResponseWriter) WriteHeader(status int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	if status == http.StatusNoContent || status == http.StatusNotModified {
		g.ResponseWriter.WriteHeader(status)
		return
	}
	g.decide()
	if g.compressed {
		g.Header().Del("Content-Length")
		g.Header().Set("Content-Encoding", "gzip")
	}
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) Write(data []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.compressed {
		return g.writer.Write(data)
	}
	return g.ResponseWriter.Write(data)
}

func (g *gzipResponseWriter) Close() {
	if g.writer == nil {
		return
	}
	_ = g.writer.Close()
	g.writer.Reset(io.Discard)
	gzipPool.Put(g.writer)
	g.writer = nil
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/api/") {
			next.ServeHTTP(response, request)
			return
		}
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		if recorder.status >= 500 || recorder.status == http.StatusUnauthorized {
			s.log.Warn("console: request failed",
				slog.String("method", request.Method),
				slog.String("path", request.URL.Path),
				slog.Int("status", recorder.status),
				slog.Duration("duration", time.Since(started)))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	encoder := json.NewEncoder(response)
	encoder.SetEscapeHTML(true)
	_ = encoder.Encode(value)
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"error": message, "code": code})
}

// Serve runs the HTTP server until the context is cancelled.
func (s *Server) Serve(ctx context.Context, listen string, onListen func(net.Addr)) error {
	if listen == "" {
		listen = s.cfg.Listen
	}
	if listen == "" {
		listen = "127.0.0.1:7331"
	}
	if !s.authRequired() && !isLoopbackListen(listen) {
		return fmt.Errorf("console: refusing to serve without a read token on non-loopback listener %q", listen)
	}
	if err := httpsecurity.ValidateListen(listen, s.tlsEnabled); err != nil {
		return fmt.Errorf("console: %w", err)
	}
	server := &http.Server{
		Addr:              listen,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16384,
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	if err := httpsecurity.ValidateListen(listener.Addr().String(), s.tlsEnabled); err != nil {
		listener.Close()
		return fmt.Errorf("console: %w", err)
	}
	if s.tlsConfig != nil {
		listener = tls.NewListener(listener, s.tlsConfig)
	}
	if onListen != nil {
		onListen(listener.Addr())
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}

func isLoopbackListen(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ── Probes ──────────────────────────────────────────────────────────────────

const probeCacheAge = 10 * time.Second

// reachabilityProbes returns bounded TCP reachability results for the
// configured infrastructure. They are labelled probes, never health claims.
func (s *Server) reachabilityProbes(ctx context.Context) []Probe {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if !s.probesAt.IsZero() && s.now().Sub(s.probesAt) < probeCacheAge {
		return s.probes
	}
	targets := []struct {
		id    string
		label string
		addr  string
	}{
		{id: "temporal", label: "Temporal", addr: s.cfg.Factory.Temporal.HostPort},
		{id: "cube", label: "CubeSandbox API", addr: cubeDialAddress(s.cfg.Factory.Cube.APIURL)},
		{id: "otel", label: "OTLP collector", addr: otlpDialAddress(s.cfg.Factory.Observability.OTLPEndpoint)},
	}
	probes := make([]Probe, 0, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target.addr) == "" {
			continue
		}
		probes = append(probes, s.probeTCP(ctx, target.id, target.label, target.addr))
	}
	s.probes = probes
	s.probesAt = s.now()
	return probes
}

func (s *Server) probeTCP(ctx context.Context, id, label, address string) Probe {
	probe := Probe{ID: id, Label: label, Target: address, State: "unknown"}
	started := time.Now()
	dial := s.cfg.ProbeDialer
	if dial == nil {
		dialer := &net.Dialer{Timeout: 2 * time.Second}
		dial = dialer.DialContext
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	connection, err := dial(probeCtx, "tcp", address)
	probe.LatencyMS = time.Since(started).Milliseconds()
	checkedAt := s.now().UTC()
	probe.CheckedAt = &checkedAt
	if err != nil {
		probe.State = "unreachable"
		probe.Detail = compactProbeError(err)
		return probe
	}
	_ = connection.Close()
	probe.State = "reachable"
	return probe
}

func compactProbeError(err error) string {
	message := err.Error()
	if strings.Contains(message, "deadline exceeded") {
		return "timed out"
	}
	if len(message) > 160 {
		return message[:160]
	}
	return message
}

func cubeDialAddress(apiURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || parsed.Host == "" {
		return ""
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(parsed.Hostname(), port)
}

func otlpDialAddress(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		port := parsed.Port()
		if port == "" {
			port = "4317"
		}
		return net.JoinHostPort(parsed.Hostname(), port)
	}
	if _, _, err := net.SplitHostPort(endpoint); err == nil {
		return endpoint
	}
	return net.JoinHostPort(endpoint, "4317")
}

func parseInt(value string, fallback, max int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	if parsed > max {
		return max
	}
	return parsed
}
