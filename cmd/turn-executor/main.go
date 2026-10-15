// turn-executor serves the private sf execution protocol over an existing Cube deployment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/turnexec"
	"net/http"
	"os"
	"os/signal"
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
	address := flag.String("listen", "127.0.0.1:8091", "private listen address; TLS required at proxy outside loopback")
	root := flag.String("state", ".factory/turn-executor", "durable private state directory (single service process)")
	profilesFile := flag.String("profiles", "", "operator profile JSON file")
	flag.Parse()
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
	service, err := turnexec.Open(startup, *root, os.Getenv("ESF_TURN_SERVICE_TOKEN"), profiles, provider)
	if err != nil {
		return err
	}
	defer service.Close()
	server := &http.Server{Addr: *address, Handler: service, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	select {
	case err = <-failures:
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
