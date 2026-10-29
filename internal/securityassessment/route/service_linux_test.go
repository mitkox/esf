package route

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGuardWireRejectsOversizedTrailingAndUnknownArguments(t *testing.T) {
	for _, data := range []string{
		`{"schema":"esf.route-guard/v1","operation":"shutdown","guard":{}}` + strings.Repeat(" ", guardMessageLimit),
		`{"schema":"esf.route-guard/v1","operation":"shutdown","guard":{}} {}`,
		`{"schema":"esf.route-guard/v1","operation":"shutdown","guard":{},"command":"anything"}`,
	} {
		var request guardRequest
		if readGuardJSON(bytes.NewBufferString(data), &request) == nil {
			t.Fatal("accepted oversized/trailing/arbitrary command input")
		}
	}
}

func TestUncertainAppliedControlRequiresCleanupWithoutFurtherKernelChanges(t *testing.T) {
	guard := HostGuard{Owner: "qualification-0123456789abcdef0123456789abcdef", IP: "192.0.2.50", Port: 9445}
	directory := t.TempDir()
	marker := filepath.Join(directory, "kernel-command-attempted")
	for _, name := range []string{"ip", "iptables", "sudo"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\nprintf attempted > '"+marker+"'\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", directory)
	state := newGuardState()
	// The ACCEPT may already exist despite a lost command acknowledgement.
	state.leases[guard.Owner] = &guardLease{guard: &guard, owned: true, expires: time.Now().Add(time.Minute), controls: map[string]guardControlState{"udp": {active: false, uncertain: true}}}
	for _, operation := range []string{"control_on", "control_off"} {
		_, _, err := state.perform(context.Background(), guardRequest{Schema: guardSchema, Operation: operation, Guard: guard, Protocol: "udp"})
		if err == nil || !strings.Contains(err.Error(), "close the owned guard") {
			t.Fatalf("uncertain mutation was retried: %v", err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("a new privileged command ran after an uncertain mutation")
	}
}

func TestGuardUnacknowledgedCreationCannotAuthorizeCleanup(t *testing.T) {
	guard := HostGuard{Owner: "qualification-0123456789abcdef0123456789abcdef", IP: "192.0.2.50", Port: 9445}
	state := newGuardState()
	state.leases[guard.Owner] = &guardLease{guard: &guard, expires: time.Now().Add(time.Minute)}
	_, _, err := state.perform(context.Background(), guardRequest{Schema: guardSchema, Operation: "close", Guard: guard})
	if err == nil || !strings.Contains(err.Error(), "ownership unacknowledged") {
		t.Fatalf("unowned creation authorized cleanup: %v", err)
	}
	if state.leases[guard.Owner].closed {
		t.Fatal("unacknowledged kernel state was marked clean")
	}
}

func guardTestSocket(t *testing.T) string {
	t.Helper()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	return filepath.Join("/tmp", "esf-issue50-guard-"+hex.EncodeToString(nonce[:]), "control.sock")
}

func TestGuardPeerPolicyDoesNotTrustOtherLocalUsers(t *testing.T) {
	for _, test := range []struct {
		peer    uint32
		allowed bool
	}{{0, true}, {1000, true}, {1001, false}} {
		if got := allowedGuardPeer(&syscall.Ucred{Uid: test.peer}, 1000); got != test.allowed {
			t.Fatalf("peer %d accepted=%v", test.peer, got)
		}
	}
	if allowedGuardPeer(nil, 1000) {
		t.Fatal("missing peer identity accepted")
	}
}

func TestGuardProtocolRejectsOperationsBeforeAnyKernelMutation(t *testing.T) {
	valid := HostGuard{Owner: "qualification-0123456789abcdef0123456789abcdef", IP: "192.0.2.50", Port: 9445}
	for _, request := range []guardRequest{
		{Schema: guardSchema, Operation: "exec", Guard: valid},
		{Schema: guardSchema, Operation: "open", Guard: HostGuard{Owner: valid.Owner, IP: "192.168.0.16", Port: 9445}},
		{Schema: guardSchema, Operation: "open", Guard: HostGuard{Owner: valid.Owner, IP: valid.IP, Port: 4000}},
		{Schema: guardSchema, Operation: "control_on", Guard: valid, Protocol: "icmp"},
		{Schema: "unknown", Operation: "open", Guard: valid},
	} {
		if validateGuardRequest(request) == nil {
			t.Fatalf("accepted unsafe request: %+v", request)
		}
	}
	state := newGuardState()
	for _, operation := range []string{"close", "control_on", "reject_count"} {
		_, _, err := state.perform(context.Background(), guardRequest{Schema: guardSchema, Operation: operation, Guard: valid, Protocol: "udp"})
		if err == nil {
			t.Fatalf("unknown owner %s was allowed to mutate or inspect state", operation)
		}
	}
}

func TestGuardSocketRefusesExistingParentsAndSymlinks(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("service root identity is exercised inside Cube")
	}
	path := guardTestSocket(t)
	parent := filepath.Dir(path)
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(parent)
	if err := ServeGuard(context.Background(), path, uint32(os.Getuid())); err == nil {
		t.Fatal("reused pre-existing socket directory")
	}
	if _, err := os.Stat(parent); err != nil {
		t.Fatal("removed unowned parent")
	}
	other := t.TempDir()
	link := guardTestSocket(t)
	if err := os.Symlink(other, filepath.Dir(link)); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Dir(link))
	if err := ServeGuard(context.Background(), link, uint32(os.Getuid())); err == nil {
		t.Fatal("accepted socket directory symlink")
	}
}

func TestGuardServicePrivateSocketRejectsUnknownOwnerAndShutsDownCleanly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("service root identity is exercised inside Cube")
	}
	path := guardTestSocket(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- serveGuard(ctx, path, 0, func() { close(ready) }) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("service did not become ready")
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket permissions: %v", info.Mode())
	}
	guard := HostGuard{Owner: "qualification-0123456789abcdef0123456789abcdef", IP: "192.0.2.50", Port: 9445}
	if _, err := CallGuard(ctx, path, "close", guard, ""); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown owner close: %v", err)
	}
	if _, err := CallGuard(ctx, path, "shutdown", HostGuard{}, ""); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not complete")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket left after shutdown: %v", err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("owned socket directory left after shutdown: %v", err)
	}
}
