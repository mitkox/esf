// Command cube-security-route-guard owns only temporary endpoint addresses and
// their firewall rules; it receives no Cube API or assessment credentials.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mitkox/esf/internal/securityassessment/route"
)

func main() {
	socket := flag.String("socket", "", "new /tmp/esf-issue50-guard-<hex>/control.sock path")
	uid := flag.String("uid", os.Getenv("SUDO_UID"), "local operator UID (default: invoking sudo user)")
	timeout := flag.Duration("timeout", 45*time.Minute, "overall lifetime, at most one hour; each guard expires after 15 minutes")
	flag.Parse()
	if err := run(*socket, *uid, *timeout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(socket, uid string, timeout time.Duration) error {
	if timeout <= 0 || timeout > time.Hour {
		return errors.New("guard service timeout must be positive and at most one hour")
	}
	parsed, err := strconv.ParseUint(uid, 10, 32)
	if err != nil {
		return errors.New("an explicit local operator UID is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return route.ServeGuard(ctx, socket, uint32(parsed))
}
