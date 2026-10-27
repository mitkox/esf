package route

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const guardSchema = "esf.route-guard/v1"
const guardMessageLimit = 4096

var guardDirectoryPattern = regexp.MustCompile(`^esf-issue50-guard-[a-f0-9]{16,64}$`)

type guardRequest struct {
	Schema    string    `json:"schema"`
	Operation string    `json:"operation"`
	Guard     HostGuard `json:"guard"`
	Protocol  string    `json:"protocol,omitempty"`
}

type guardResponse struct {
	Schema string `json:"schema"`
	Owner  string `json:"owner,omitempty"`
	Count  uint64 `json:"count,omitempty"`
	Error  string `json:"error,omitempty"`
}

type guardControlState struct{ active, uncertain bool }
type guardLease struct {
	guard    *HostGuard
	expires  time.Time
	closed   bool
	owned    bool
	controls map[string]guardControlState
}
type guardState struct{ leases map[string]*guardLease }

func newGuardState() *guardState { return &guardState{leases: map[string]*guardLease{}} }

func validateGuardSocket(path string) error {
	if filepath.Clean(path) != path || filepath.Base(path) != "control.sock" || filepath.Dir(filepath.Dir(path)) != "/tmp" || !guardDirectoryPattern.MatchString(filepath.Base(filepath.Dir(path))) {
		return errors.New("guard socket requires a new /tmp/esf-issue50-guard-<hex>/control.sock path")
	}
	return nil
}

func validateGuardRequest(request guardRequest) error {
	if request.Schema != guardSchema {
		return errors.New("unsupported guard protocol")
	}
	switch request.Operation {
	case "shutdown":
		if request.Guard.Owner != "" || request.Guard.IP != "" || request.Guard.Port != 0 || request.Protocol != "" {
			return errors.New("shutdown takes no guard arguments")
		}
		return nil
	case "open", "close", "reject_count":
		if request.Protocol != "" {
			return errors.New("unexpected guard protocol argument")
		}
	case "control_on", "control_off":
		if request.Protocol != "tcp" && request.Protocol != "udp" {
			return errors.New("unsupported control protocol")
		}
	default:
		return errors.New("unsupported guard operation")
	}
	if err := request.Guard.validate(); err != nil {
		return err
	}
	if request.Guard.Port != 9445 {
		return errors.New("guard service permits only the fixed endpoint port")
	}
	return nil
}

func allowedGuardPeer(credential *syscall.Ucred, uid uint32) bool {
	return credential != nil && (credential.Uid == 0 || credential.Uid == uid)
}

func guardPeer(connection *net.UnixConn) (*syscall.Ucred, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return nil, err
	}
	var credential *syscall.Ucred
	var inspectErr error
	if err := raw.Control(func(fd uintptr) {
		credential, inspectErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return nil, err
	}
	return credential, inspectErr
}

func sameGuard(left, right HostGuard) bool {
	return left.Owner == right.Owner && left.IP == right.IP && left.Port == right.Port
}

func (state *guardState) perform(ctx context.Context, request guardRequest) (uint64, bool, error) {
	if err := validateGuardRequest(request); err != nil {
		return 0, false, err
	}
	if request.Operation == "shutdown" {
		err := state.closeAll(ctx)
		return 0, err == nil, err
	}
	lease, known := state.leases[request.Guard.Owner]
	if known && !sameGuard(*lease.guard, request.Guard) {
		return 0, false, errors.New("guard owner arguments changed")
	}
	if request.Operation == "open" {
		if known {
			return 0, false, errors.New("guard owner already registered; allocation is never retried")
		}
		active := 0
		for _, lease := range state.leases {
			if !lease.closed {
				active++
			}
		}
		if active >= 4 || len(state.leases) >= 64 {
			return 0, false, errors.New("guard service attempt budget exhausted")
		}
		for _, lease := range state.leases {
			if !lease.closed && lease.guard.IP == request.Guard.IP {
				return 0, false, errors.New("endpoint address already registered")
			}
		}
		local := &HostGuard{Owner: request.Guard.Owner, IP: request.Guard.IP, Port: request.Guard.Port}
		lease = &guardLease{guard: local, expires: time.Now().Add(15 * time.Minute), controls: map[string]guardControlState{}}
		state.leases[local.Owner] = lease
		err := local.Open(ctx)
		lease.owned = local.created
		return 0, false, err
	}
	if !known {
		return 0, false, errors.New("unknown guard owner; no kernel state was changed")
	}
	if !lease.owned {
		return 0, false, errors.New("guard creation ownership unacknowledged; kernel state left untouched")
	}
	if request.Operation == "close" {
		if lease.closed {
			return 0, false, nil
		}
		err := lease.guard.Close(ctx)
		if err == nil {
			lease.closed = true
		}
		return 0, false, err
	}
	if lease.closed {
		return 0, false, errors.New("guard owner is already closed")
	}
	if time.Now().After(lease.expires) {
		return 0, false, errors.New("guard lease expired")
	}
	if request.Operation == "reject_count" {
		type rejectCounter interface {
			UDPRejectCount(context.Context) (uint64, error)
		}
		counter, ok := any(lease.guard).(rejectCounter)
		if !ok {
			return 0, false, errors.New("guard counter operation unavailable")
		}
		count, err := counter.UDPRejectCount(ctx)
		return count, false, err
	}
	enabled := request.Operation == "control_on"
	previous := lease.controls[request.Protocol]
	if previous.uncertain {
		return 0, false, errors.New("guard control outcome uncertain; close the owned guard before further controls")
	}
	if !previous.uncertain && previous.active == enabled {
		return 0, false, nil
	}
	lease.controls[request.Protocol] = guardControlState{active: previous.active, uncertain: true}
	err := lease.guard.SetControl(ctx, request.Protocol, enabled)
	if err == nil {
		lease.controls[request.Protocol] = guardControlState{active: enabled}
	}
	return 0, false, err
}

func (state *guardState) closeAll(ctx context.Context) error {
	var failures error
	for _, lease := range state.leases {
		if lease.closed {
			continue
		}
		if !lease.owned {
			failures = errors.Join(failures, errors.New("guard creation ownership unacknowledged"))
			continue
		}
		if err := lease.guard.Close(ctx); err != nil {
			failures = errors.Join(failures, err)
		} else {
			lease.closed = true
		}
	}
	return failures
}

func (state *guardState) expire(ctx context.Context) error {
	var failures error
	for _, lease := range state.leases {
		if lease.closed || time.Now().Before(lease.expires) {
			continue
		}
		if !lease.owned {
			failures = errors.Join(failures, errors.New("guard creation ownership unacknowledged"))
			continue
		}
		if err := lease.guard.Close(ctx); err != nil {
			failures = errors.Join(failures, err)
		} else {
			lease.closed = true
		}
	}
	return failures
}

// ServeGuard exposes only the bounded endpoint guard operations to one local
// operator UID. The root-owned, exclusive socket directory is never reused.
func ServeGuard(ctx context.Context, socketPath string, allowedUID uint32) error {
	return serveGuard(ctx, socketPath, allowedUID, func() { fmt.Fprintln(os.Stdout, "ready") })
}

func serveGuard(ctx context.Context, socketPath string, allowedUID uint32, ready func()) (returnErr error) {
	ctx, cancelLifetime := context.WithTimeout(ctx, time.Hour)
	defer cancelLifetime()
	if os.Geteuid() != 0 {
		return errors.New("the scoped guard service must run as root")
	}
	if err := validateGuardSocket(socketPath); err != nil {
		return err
	}
	parent := filepath.Dir(socketPath)
	if err := os.Mkdir(parent, 0711); err != nil {
		return errors.New("guard socket directory must not already exist")
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	defer func() {
		if current, err := os.Lstat(parent); err == nil && os.SameFile(parentInfo, current) {
			returnErr = errors.Join(returnErr, os.Remove(parent))
		}
	}()
	if err := os.Chmod(parent, 0711); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return err
	}
	listener.SetUnlinkOnClose(false)
	defer listener.Close()
	socketInfo, err := os.Lstat(socketPath)
	if err != nil {
		return err
	}
	defer func() {
		if current, err := os.Lstat(socketPath); err == nil && os.SameFile(socketInfo, current) {
			returnErr = errors.Join(returnErr, os.Remove(socketPath))
		}
	}()
	if err := os.Chmod(socketPath, 0600); err != nil {
		return err
	}
	if err := os.Chown(socketPath, int(allowedUID), -1); err != nil {
		return err
	}
	state := newGuardState()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		returnErr = errors.Join(returnErr, state.closeAll(cleanup))
	}()
	if ready != nil {
		ready()
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		err := state.expire(cleanup)
		cancel()
		if err != nil {
			return err
		}
		if err := listener.SetDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			return err
		}
		connection, err := listener.AcceptUnix()
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		stop := func() bool {
			defer connection.Close()
			credential, err := guardPeer(connection)
			if err != nil || !allowedGuardPeer(credential, allowedUID) {
				return false
			}
			deadline := time.Now().Add(25 * time.Second)
			if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
				deadline = outer
			}
			for _, lease := range state.leases {
				if !lease.closed && lease.expires.Before(deadline) {
					deadline = lease.expires
				}
			}
			connection.SetDeadline(deadline)
			var request guardRequest
			if err := readGuardJSON(connection, &request); err != nil {
				writeGuardResponse(connection, guardResponse{Schema: guardSchema, Error: "malformed guard request"})
				return false
			}
			operation, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			count, stop, err := state.perform(operation, request)
			response := guardResponse{Schema: guardSchema, Owner: request.Guard.Owner, Count: count}
			if err != nil {
				response.Error = err.Error()
				if len(response.Error) > 256 {
					response.Error = response.Error[:256]
				}
			}
			writeGuardResponse(connection, response)
			return stop
		}()
		if stop {
			return nil
		}
	}
}

func writeGuardResponse(writer io.Writer, response guardResponse) {
	data, err := json.Marshal(response)
	if err == nil && len(data) <= guardMessageLimit {
		_, _ = writer.Write(append(data, '\n'))
	}
}

func readGuardJSON(reader io.Reader, destination any) error {
	data, err := io.ReadAll(io.LimitReader(reader, guardMessageLimit+1))
	if err != nil || len(data) == 0 || len(data) > guardMessageLimit {
		return errors.New("guard message missing, oversized or unavailable")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing guard message")
	}
	return nil
}

// CallGuard verifies the root server's kernel peer identity before sending any
// request. It cannot submit commands, paths or arbitrary ports to the helper.
func CallGuard(ctx context.Context, socketPath, operation string, guard HostGuard, protocol string) (uint64, error) {
	if err := validateGuardSocket(socketPath); err != nil {
		return 0, err
	}
	request := guardRequest{Schema: guardSchema, Operation: operation, Guard: HostGuard{Owner: guard.Owner, IP: guard.IP, Port: guard.Port}, Protocol: protocol}
	if err := validateGuardRequest(request); err != nil {
		return 0, err
	}
	data, err := json.Marshal(request)
	if err != nil || len(data) > guardMessageLimit {
		return 0, errors.New("guard request exceeded bound")
	}
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return 0, errors.New("scoped root guard service unavailable")
	}
	defer connection.Close()
	unix, ok := connection.(*net.UnixConn)
	if !ok {
		return 0, errors.New("guard connection is not a local Unix socket")
	}
	credential, err := guardPeer(unix)
	if err != nil || credential == nil || credential.Uid != 0 {
		return 0, errors.New("guard server is not owned by root")
	}
	deadline := time.Now().Add(25 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := unix.SetDeadline(deadline); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { unix.Close() })
	defer stop()
	if _, err := unix.Write(append(data, '\n')); err != nil {
		return 0, errors.New("guard acknowledgement unavailable")
	}
	if err := unix.CloseWrite(); err != nil {
		return 0, err
	}
	var response guardResponse
	if err := readGuardJSON(unix, &response); err != nil {
		return 0, errors.New("guard acknowledgement malformed or unavailable")
	}
	if response.Schema != guardSchema || response.Owner != request.Guard.Owner {
		return 0, errors.New("guard acknowledgement identity mismatch")
	}
	if response.Error != "" {
		return 0, errors.New(strings.TrimSpace(response.Error))
	}
	return response.Count, nil
}
