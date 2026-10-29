//go:build !linux

package route

import (
	"context"
	"errors"
)

// ErrHostGuardUnsupported means no endpoint firewall operation is available.
var ErrHostGuardUnsupported = errors.New("endpoint host guard requires Linux")

// CheckHostGuardSupport reports whether this platform can enforce the guard.
func CheckHostGuardSupport() error { return ErrHostGuardUnsupported }

func (*HostGuard) Open(context.Context) error                    { return ErrHostGuardUnsupported }
func (HostGuard) Close(context.Context) error                    { return ErrHostGuardUnsupported }
func (HostGuard) SetControl(context.Context, string, bool) error { return ErrHostGuardUnsupported }
func (HostGuard) UDPRejectCount(context.Context) (uint64, error) { return 0, ErrHostGuardUnsupported }

// ServeGuard never starts a service on an unsupported platform.
func ServeGuard(context.Context, string, uint32) error { return ErrHostGuardUnsupported }

// CallGuard never contacts a socket on an unsupported platform.
func CallGuard(context.Context, string, string, HostGuard, string) (uint64, error) {
	return 0, ErrHostGuardUnsupported
}
