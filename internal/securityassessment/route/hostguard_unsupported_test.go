//go:build !linux

package route

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedHostGuardRefusesEveryOperation(t *testing.T) {
	ctx := context.Background()
	guard := HostGuard{}
	_, counterErr := guard.UDPRejectCount(ctx)
	_, callErr := CallGuard(ctx, "/unavailable/control.sock", "open", guard, "")
	for _, err := range []error{
		CheckHostGuardSupport(), guard.Open(ctx), guard.Close(ctx),
		guard.SetControl(ctx, "tcp", true), counterErr, callErr,
		ServeGuard(ctx, "/unavailable/control.sock", 0),
	} {
		if !errors.Is(err, ErrHostGuardUnsupported) {
			t.Fatalf("unsupported operation did not fail closed: %v", err)
		}
	}
}
