package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/httpsecurity"
	"github.com/mitkox/esf/internal/secretfile"
)

// Config configures one execution API listener.
type Config struct {
	// Listen is the address the API binds. Credentials are never carried over a
	// remote plaintext listener.
	Listen string
	// TokenFile is the operator-owned bearer token file. It is read through the
	// private-file reader at startup.
	TokenFile string
	// TLSCertFile and TLSKeyFile enable native HTTPS. Both are required
	// together; an empty pair serves loopback HTTP for a TLS-terminating proxy.
	TLSCertFile string
	TLSKeyFile  string
	// Controller executes the API's operations.
	Controller Controller
}

// Server is the public execution API.
type Server struct {
	controller Controller
	listen     string
	token      string
	tlsConfig  *tls.Config
	handler    http.Handler
}

// New reads the bearer token, loads TLS material and validates the listen
// address before any socket is opened.
func New(cfg Config) (*Server, error) {
	if cfg.Controller == nil {
		return nil, errors.New("api: an execution controller is required")
	}
	if strings.TrimSpace(cfg.TokenFile) == "" {
		return nil, errors.New("api: a token file is required")
	}
	body, err := secretfile.Read(cfg.TokenFile, 4096)
	if err != nil {
		return nil, fmt.Errorf("api: read token file: %w", err)
	}
	token := strings.TrimSpace(string(body))
	if len(token) < 32 || !httpsecurity.ValidBearerToken(token) {
		return nil, errors.New("api: the bearer token must contain at least 32 characters from the ASCII bearer token alphabet")
	}
	tlsConfig, err := httpsecurity.LoadTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	if err := httpsecurity.ValidateListen(cfg.Listen, tlsConfig != nil); err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	server := &Server{
		controller: cfg.Controller,
		listen:     cfg.Listen,
		token:      token,
		tlsConfig:  tlsConfig,
	}
	server.handler = server.routes()
	return server, nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", s.handleSubmit)
	mux.HandleFunc("GET /v1/runs/{run_id}", s.handleStatus)
	mux.HandleFunc("GET /v1/runs/{run_id}/events", s.handleEvents)
	mux.HandleFunc("GET /v1/runs/{run_id}/evidence", s.handleEvidence)
	mux.HandleFunc("POST /v1/runs/{run_id}/cancel", s.handleCancel)
	mux.HandleFunc("/", s.handleNotFound)
	return s.authenticate(mux)
}

// authenticate applies the shared bearer rules to every request, including
// unknown paths, so the listener never answers before checking the caller.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !httpsecurity.Authenticated(request, s.token) {
			response.Header().Set("WWW-Authenticate", `Bearer realm="ESF execution API"`)
			writeError(response, http.StatusUnauthorized, codeUnauthorized, "A valid execution API bearer token is required.")
			return
		}
		next.ServeHTTP(response, request)
	})
}

// Serve binds the listener and serves until the context is cancelled. It
// re-validates the address that was actually bound, not only the one requested.
func (s *Server) Serve(ctx context.Context, onListen func(net.Addr)) error {
	if err := httpsecurity.ValidateListen(s.listen, s.tlsConfig != nil); err != nil {
		return fmt.Errorf("api: %w", err)
	}
	server := &http.Server{
		Addr:              s.listen,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16384,
	}
	listener, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := httpsecurity.ValidateListen(listener.Addr().String(), s.tlsConfig != nil); err != nil {
		return fmt.Errorf("api: %w", err)
	}
	if s.tlsConfig != nil {
		listener = tls.NewListener(listener, s.tlsConfig)
	}
	if onListen != nil {
		onListen(listener.Addr())
	}
	failures := make(chan error, 1)
	go func() { failures <- server.Serve(listener) }()
	select {
	case err := <-failures:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return server.Shutdown(shutdown)
	}
}
