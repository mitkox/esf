package route

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// CheckHostGuardSupport reports whether this platform can enforce the guard.
func CheckHostGuardSupport() error { return nil }

func (g HostGuard) validate() error {
	suffix, ok := strings.CutPrefix(g.Owner, "qualification-")
	if !ok || len(suffix) != 32 {
		return errors.New("invalid route owner")
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return errors.New("invalid route owner")
	}
	ip, err := netip.ParseAddr(g.IP)
	if err != nil || !netip.MustParsePrefix("192.0.2.0/24").Contains(ip) || ip == netip.MustParseAddr("192.0.2.0") || ip == netip.MustParseAddr("192.0.2.255") {
		return errors.New("route address must be an exclusive TEST-NET /32")
	}
	if g.Port < 1024 || g.Port > 65535 {
		return errors.New("invalid route listener port")
	}
	return nil
}

func (g HostGuard) chain() string {
	return "ESF50_" + strings.TrimPrefix(g.Owner, "qualification-")[:16]
}
func (g HostGuard) label() string {
	return "lo:e50" + strings.TrimPrefix(g.Owner, "qualification-")[:8]
}
func (g HostGuard) jump() []string {
	return []string{"INPUT", "-d", g.IP + "/32", "-m", "comment", "--comment", g.Owner, "-j", g.chain()}
}

func privileged(ctx context.Context, name string, args ...string) ([]byte, error) {
	if os.Geteuid() != 0 {
		args = append([]string{"-n", name}, args...)
		name = "sudo"
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("route infrastructure command %s failed: %w", name, err)
	}
	if len(out) > 1<<20 {
		return nil, errors.New("route infrastructure output exceeded bound")
	}
	return out, nil
}

type linkAddresses struct {
	Addresses []struct {
		Local string `json:"local"`
		Label string `json:"label"`
	} `json:"addr_info"`
}

func (g HostGuard) addressOwner(ctx context.Context) (string, bool, error) {
	out, err := privileged(ctx, "ip", "-j", "addr", "show", "dev", "lo")
	if err != nil {
		return "", false, err
	}
	var links []linkAddresses
	if err := json.Unmarshal(out, &links); err != nil {
		return "", false, err
	}
	for _, link := range links {
		for _, address := range link.Addresses {
			if address.Local == g.IP {
				return address.Label, true, nil
			}
		}
	}
	return "", false, nil
}

// Open creates the guard before making the address reachable. The chain is
// exclusive; no existing chain, address, firewall defaults or routes are reused.
func (g *HostGuard) Open(ctx context.Context) error {
	if err := g.validate(); err != nil {
		return err
	}
	if g.Socket != "" {
		_, err := CallGuard(ctx, g.Socket, "open", *g, "")
		return err
	}
	if _, exists, err := g.addressOwner(ctx); err != nil {
		return err
	} else if exists {
		return errors.New("endpoint address already exists; refusing reuse")
	}
	if _, err := privileged(ctx, "iptables", "-w", "5", "-N", g.chain()); err != nil {
		return err
	}
	g.created = true
	for _, args := range [][]string{
		{"-A", g.chain(), "-m", "comment", "--comment", g.Owner, "-j", "REJECT", "--reject-with", "icmp-port-unreachable"},
		{"-I", g.chain(), "1", "-p", "udp", "--dport", fmt.Sprint(g.Port), "-m", "comment", "--comment", g.Owner + "-udp-guard", "-j", "REJECT", "--reject-with", "icmp-port-unreachable"},
		{"-I", g.chain(), "1", "-p", "tcp", "--dport", fmt.Sprint(g.Port), "-m", "comment", "--comment", g.Owner, "-j", "ACCEPT"},
		append([]string{"-I", "INPUT", "1"}, g.jump()[1:]...),
	} {
		if _, err := privileged(ctx, "iptables", append([]string{"-w", "5"}, args...)...); err != nil {
			return err
		}
	}
	_, err := privileged(ctx, "ip", "addr", "add", g.IP+"/32", "dev", "lo", "label", g.label())
	return err
}

// SetControl temporarily permits the fixed owned reachability fixture. The
// exception is removed before sending the same identified packet again.
func (g HostGuard) SetControl(ctx context.Context, protocol string, enabled bool) error {
	if err := g.validate(); err != nil {
		return err
	}
	if g.Socket != "" {
		operation := "control_off"
		if enabled {
			operation = "control_on"
		}
		_, err := CallGuard(ctx, g.Socket, operation, g, protocol)
		return err
	}
	port := g.Port
	if protocol == "tcp" {
		port++
	} else if protocol != "udp" {
		return errors.New("invalid control protocol")
	}
	label, exists, err := g.addressOwner(ctx)
	if err != nil {
		return err
	}
	if !exists || label != g.label() {
		return errors.New("control address ownership mismatch")
	}
	operation := "-D"
	if enabled {
		operation = "-I"
	}
	args := []string{"-w", "5", operation, g.chain()}
	if enabled {
		args = append(args, "1")
	}
	args = append(args, "-p", protocol, "--dport", fmt.Sprint(port), "-m", "comment", "--comment", g.Owner+"-control-"+protocol, "-j", "ACCEPT")
	_, err = privileged(ctx, "iptables", args...)
	return err
}

// Close also handles partial Open and process interruption. It never removes an
// address labeled by another owner, even when the saved receipt is stale.
func (g HostGuard) Close(ctx context.Context) error {
	if err := g.validate(); err != nil {
		return err
	}
	if g.Socket != "" {
		_, err := CallGuard(ctx, g.Socket, "close", g, "")
		return err
	}
	var failures error
	chainRules, chainErr := privileged(ctx, "iptables", "-w", "5", "-S", g.chain())
	if chainErr != nil {
		var exit *exec.ExitError
		if !errors.As(chainErr, &exit) || exit.ExitCode() != 1 {
			return chainErr
		}
		if _, exists, err := g.addressOwner(ctx); err != nil {
			return err
		} else if exists {
			return errors.New("cannot establish full ownership of endpoint address without chain")
		}
		return nil
	}
	if !g.ownsRules(string(chainRules)) {
		if !g.created || strings.Contains(string(chainRules), "-A ") {
			return errors.New("endpoint firewall chain ownership mismatch")
		}
	}
	label, exists, err := g.addressOwner(ctx)
	if err != nil {
		return err
	} else if exists {
		if label != g.label() {
			return errors.New("endpoint address belongs to another owner")
		}
		if _, err := privileged(ctx, "ip", "addr", "del", g.IP+"/32", "dev", "lo"); err != nil {
			return err
		}
	}
	if _, exists, err := g.addressOwner(ctx); err != nil {
		return err
	} else if exists {
		return errors.New("endpoint address remains; firewall protection retained")
	}
	// Missing owned rules are already reconciled; other errors remain visible.
	for _, args := range [][]string{append([]string{"-D"}, g.jump()...), {"-F", g.chain()}, {"-X", g.chain()}} {
		_, err := privileged(ctx, "iptables", append([]string{"-w", "5"}, args...)...)
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				failures = errors.Join(failures, err)
			}
		}
	}
	if _, exists, err := g.addressOwner(ctx); err != nil {
		failures = errors.Join(failures, err)
	} else if exists {
		failures = errors.Join(failures, errors.New("endpoint address remains"))
	}
	if _, err := privileged(ctx, "iptables", "-w", "5", "-S", g.chain()); err == nil {
		failures = errors.Join(failures, errors.New("endpoint firewall chain remains"))
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			failures = errors.Join(failures, err)
		}
	}
	return failures
}

func (g HostGuard) ownsRules(rules string) bool {
	found := false
	for _, line := range strings.Split(rules, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "-A" {
			continue
		}
		if fields[1] != g.chain() {
			return false
		}
		owner := ""
		for i, field := range fields {
			if field == "--comment" && i+1 < len(fields) {
				owner = strings.Trim(fields[i+1], "\"'")
			}
		}
		if owner != g.Owner && owner != g.Owner+"-control-tcp" && owner != g.Owner+"-control-udp" && owner != g.Owner+"-udp-guard" {
			return false
		}
		if owner == g.Owner {
			found = true
		}
	}
	return found
}

func (g HostGuard) UDPRejectCount(ctx context.Context) (uint64, error) {
	if err := g.validate(); err != nil {
		return 0, err
	}
	if g.Socket != "" {
		return CallGuard(ctx, g.Socket, "reject_count", g, "")
	}
	rules, err := privileged(ctx, "iptables", "-w", "5", "-S", g.chain())
	if err != nil {
		return 0, err
	}
	if !g.ownsRules(string(rules)) {
		return 0, errors.New("endpoint firewall ownership mismatch")
	}
	output, err := privileged(ctx, "iptables", "-w", "5", "-n", "-v", "-x", "-L", g.chain())
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && fields[2] == "REJECT" && fields[3] == "udp" && strings.Contains(line, g.Owner+"-udp-guard") {
			return strconv.ParseUint(fields[0], 10, 64)
		}
	}
	return 0, errors.New("owned UDP guard counter unavailable")
}
