// turn-executor serves the private sf execution protocol over an existing Cube deployment.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/mitkox/esf/internal/httpsecurity"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/secretfile"
	"github.com/mitkox/esf/internal/turnexec"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	address := flag.String("listen", "127.0.0.1:8091", "listen address (non-loopback requires TLS)")
	root := flag.String("state", ".factory/turn-executor", "durable private state directory (single service process)")
	profilesFile := flag.String("profiles", "", "operator profile JSON file")
	certFile := flag.String("tls-cert-file", "", "PEM certificate for native HTTPS (requires --tls-key-file)")
	keyFile := flag.String("tls-key-file", "", "private 0600 PEM key for native HTTPS")
	tokenFile := flag.String("token-file", "", "private 0600 service token file; overrides ESF_TURN_SERVICE_TOKEN")
	flag.Parse()
	tlsConfig, err := httpsecurity.LoadTLSConfig(*certFile, *keyFile)
	if err != nil {
		return err
	}
	if err := httpsecurity.ValidateListen(*address, tlsConfig != nil); err != nil {
		return err
	}
	token, err := loadServiceToken(*tokenFile)
	if err != nil {
		return err
	}
	if *profilesFile == "" {
		return errors.New("--profiles is required")
	}
	f, err := os.Open(*profilesFile)
	if err != nil {
		return err
	}
	defer f.Close()
	profiles := map[string]turnexec.Profile{}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&profiles); err != nil {
		return err
	}
	provider, err := cube.New(cube.ConfigFromEnv())
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	startup, startCancel := context.WithTimeout(ctx, 60*time.Second)
	defer startCancel()
	if err = provider.Ping(startup); err != nil {
		return err
	}
	service, err := turnexec.Open(startup, *root, token, profiles, provider)
	if err != nil {
		return err
	}
	defer service.Close()
	return serve(ctx, *address, tlsConfig, service, nil)
}

// serve is the command's transport path, also exercised by HTTPS regressions.
func serve(ctx context.Context, address string, tlsConfig *tls.Config, handler http.Handler, onListen func(net.Addr)) error {
	if err := httpsecurity.ValidateListen(address, tlsConfig != nil); err != nil {
		return err
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	failures := make(chan error, 1)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := httpsecurity.ValidateListen(listener.Addr().String(), tlsConfig != nil); err != nil {
		return err
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	if onListen != nil {
		onListen(listener.Addr())
	}
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

func loadServiceToken(tokenFile string) (string, error) {
	token := os.Getenv("ESF_TURN_SERVICE_TOKEN")
	if tokenFile != "" {
		body, err := secretfile.Read(tokenFile, 4096)
		if err != nil {
			return "", fmt.Errorf("read service token: %w", err)
		}
		token = strings.TrimSpace(string(body))
	}
	if len(token) < 32 || !httpsecurity.ValidBearerToken(token) {
		return "", errors.New("service token must contain at least 32 characters from the ASCII bearer token alphabet")
	}
	return token, nil
}
