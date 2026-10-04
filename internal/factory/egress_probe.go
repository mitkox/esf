package factory

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// EgressProbeResult is the durable record of what a run's sandbox could reach.
//
// The factory previously recorded only the network policy it REQUESTED. A
// policy that is silently overridden by a deployment or template is worse than
// no policy, because it produces false confidence in the evidence. This result
// is the measurement that makes the evidence honest.
type EgressProbeResult struct {
	// Policy names the egress policy the probe was verifying.
	Policy string `json:"policy,omitempty"`
	// ExpectDeny records whether the factory believed public egress was blocked.
	ExpectDeny bool `json:"expect_deny"`
	// Canary is the public endpoint that was probed.
	Canary ProbeObservation `json:"canary"`
	// Metadata is the cloud instance-metadata endpoint that was probed.
	Metadata ProbeObservation `json:"metadata"`
	// AgentUID is the numeric uid the agent would run as. -1 when unknown.
	AgentUID int `json:"agent_uid"`
	// AgentUser is the resolved user name, best effort.
	AgentUser string `json:"agent_user,omitempty"`
	// Violations lists every invariant the probe proved false. A non-empty list
	// is why a run fails closed.
	Violations []string `json:"violations,omitempty"`
	// Notes lists conditions the probe could not decide.
	Notes []string `json:"notes,omitempty"`
	// ProbedAt is wall-clock time inside the activity. Unlike workflow time it
	// is not replayed, and it is evidence, never a decision input.
	ProbedAt time.Time `json:"probed_at"`
	// Duration is how long the probe took.
	Duration time.Duration `json:"duration"`
	// Raw is the probe's bounded raw output, for diagnosis.
	Raw string `json:"raw,omitempty"`
}

// ProbeObservation is one probed destination.
type ProbeObservation struct {
	URL       string `json:"url"`
	Reachable bool   `json:"reachable"`
	Detail    string `json:"detail,omitempty"`
	// Decided is false when the probe could not determine reachability (for
	// example, no HTTP client and no /dev/tcp support). An undecided probe
	// never fails a run, but it is recorded so the gap is visible.
	Decided bool `json:"decided"`
}

// egressProbeScript is factory-owned shell code, never caller-supplied.
//
// It emits one machine-readable line per observation on stdout:
//
//	ESF_PROBE identity <uid> <user>
//	ESF_PROBE canary <reachable|blocked|unknown> <detail>
//	ESF_PROBE metadata <reachable|blocked|unknown> <detail>
//
// Reachability means "an HTTP response arrived", not "the response was 2xx": a
// 401 from a metadata service still proves the network path exists. The script
// prefers curl and falls back to bash's /dev/tcp, because the factory cannot
// assume either is present in an arbitrary template.
const egressProbeScript = `set -u
probe_emit() { printf 'ESF_PROBE %s %s %s\n' "$1" "$2" "$3"; }

uid=$(id -u 2>/dev/null || echo unknown)
uname_s=$(id -un 2>/dev/null || echo unknown)
probe_emit identity "$uid" "$uname_s"

probe_http() {
  name="$1"; url="$2"; host="$3"; port="$4"
  if command -v curl >/dev/null 2>&1; then
    code=$(curl -sS -o /dev/null --max-time 6 -w '%{http_code}' "$url" 2>/dev/null)
    rc=$?
    if [ "$rc" -eq 0 ] && [ -n "$code" ] && [ "$code" != "000" ]; then
      probe_emit "$name" reachable "http_$code"
      return
    fi
    # DNS, TLS, tool, and timeout failures cannot establish a denied boundary.
    probe_emit "$name" unknown "curl_rc=$rc"
    return
  fi
  if command -v timeout >/dev/null 2>&1 && [ -x /bin/bash ]; then
    if timeout 6 /bin/bash -c "exec 3<>/dev/tcp/$host/$port" 2>/dev/null; then
      probe_emit "$name" reachable "tcp_connect"
    else
      probe_emit "$name" unknown "tcp_failed"
    fi
    return
  fi
  probe_emit "$name" unknown "no_http_client"
}

probe_http canary "__ESF_CANARY_URL__" "__ESF_CANARY_HOST__" __ESF_CANARY_PORT__
probe_http metadata "__ESF_METADATA_URL__" "__ESF_METADATA_HOST__" __ESF_METADATA_PORT__
exit 0
`

// renderEgressProbeScript substitutes the configured targets into the
// factory-owned script. Values come from operator configuration that
// validateProbeURL has already parsed, so they cannot contain shell
// metacharacters that matter.
func renderEgressProbeScript(canary, metadata string) (string, error) {
	canaryHost, canaryPort, err := hostPortFor(canary, "443")
	if err != nil {
		return "", fmt.Errorf("egress canary: %w", err)
	}
	metadataHost, metadataPort, err := hostPortFor(metadata, "80")
	if err != nil {
		return "", fmt.Errorf("metadata endpoint: %w", err)
	}
	script := egressProbeScript
	replacements := []struct{ key, value string }{
		{"__ESF_CANARY_URL__", canary},
		{"__ESF_CANARY_HOST__", canaryHost},
		{"__ESF_CANARY_PORT__", canaryPort},
		{"__ESF_METADATA_URL__", metadata},
		{"__ESF_METADATA_HOST__", metadataHost},
		{"__ESF_METADATA_PORT__", metadataPort},
	}
	for _, r := range replacements {
		// The host and port are interpolated UNQUOTED into the probe line, so
		// anything the shell could reinterpret is refused here. Operator
		// configuration is trusted, but a URL is data, and the cost of this
		// check is zero.
		if strings.ContainsAny(r.value, "'\"\n\r\t ;$`\\*?[]{}()&|<>~^!#%") {
			return "", fmt.Errorf("probe target %q contains shell metacharacters", r.value)
		}
		script = strings.ReplaceAll(script, r.key, r.value)
	}
	return script, nil
}

// hostPortFor extracts the host and port for the /dev/tcp fallback.
//
// The scheme decides the default port; fallbackPort applies only when the URL
// carries no scheme, so a caller cannot accidentally probe 443 for a plaintext
// endpoint.
func hostPortFor(rawURL, fallbackPort string) (string, string, error) {
	host := rawURL
	defaultPort := fallbackPort
	if i := strings.Index(host, "://"); i >= 0 {
		switch strings.ToLower(host[:i]) {
		case "https":
			defaultPort = "443"
		case "http":
			defaultPort = "80"
		}
		host = host[i+3:]
	}
	port := defaultPort
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		candidate := host[i+1:]
		if _, err := strconv.Atoi(candidate); err == nil {
			port = candidate
			host = host[:i]
		}
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "" {
		return "", "", fmt.Errorf("no host in %q", rawURL)
	}
	return host, port, nil
}

// parseEgressProbe reads the probe's stdout. Unknown lines are ignored so a
// template that prints a banner cannot corrupt the result.
func parseEgressProbe(canary, metadata string, out string) EgressProbeResult {
	result := EgressProbeResult{
		Canary:   ProbeObservation{URL: canary},
		Metadata: ProbeObservation{URL: metadata},
		AgentUID: -1,
	}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "ESF_PROBE ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		name, verdict, detail := fields[1], fields[2], strings.Join(fields[3:], " ")
		switch name {
		case "identity":
			if uid, err := strconv.Atoi(verdict); err == nil {
				result.AgentUID = uid
			}
			result.AgentUser = detail
		case "canary":
			result.Canary = ProbeObservation{URL: canary, Reachable: verdict == "reachable", Decided: verdict == "reachable" || verdict == "blocked", Detail: detail}
		case "metadata":
			result.Metadata = ProbeObservation{URL: metadata, Reachable: verdict == "reachable", Decided: verdict == "reachable" || verdict == "blocked", Detail: detail}
		}
	}
	return result
}

// evaluateEgressProbe turns observations into invariant violations.
//
// The rules are deliberately narrow. A probe can prove that a boundary is OPEN
// (a connection succeeded); it cannot prove a boundary is closed (a timeout may
// be a proxy, a DNS failure, or a firewall). So only positive reachability
// against an expected-deny posture fails a run, and everything undecided is a
// note.
func evaluateEgressProbe(result EgressProbeResult, requireNonRoot bool) EgressProbeResult {
	var violations []string
	var notes []string

	if result.Canary.Reachable && result.ExpectDeny {
		violations = append(violations, fmt.Sprintf(
			"egress policy %q claims to deny public internet, but the sandbox reached %s (%s)",
			result.Policy, result.Canary.URL, result.Canary.Detail))
	}
	if result.Metadata.Reachable {
		violations = append(violations, fmt.Sprintf(
			"the sandbox reached the cloud metadata endpoint %s (%s); metadata credentials need no third-party compromise",
			result.Metadata.URL, result.Metadata.Detail))
	}
	if requireNonRoot {
		switch {
		case result.AgentUID < 0:
			notes = append(notes, "could not determine the sandbox uid; require_non_root is not verified")
		case result.AgentUID == 0:
			violations = append(violations, "the agent would execute as uid 0 (root) inside the sandbox")
		}
	}
	if !result.Canary.Decided {
		notes = append(notes, "the public-internet probe was undecided (inconclusive): "+result.Canary.Detail)
	}
	if !result.Metadata.Decided {
		notes = append(notes, "the metadata probe was undecided (inconclusive): "+result.Metadata.Detail)
	}
	if !result.Canary.Reachable && !result.ExpectDeny {
		notes = append(notes, "the sandbox could not reach the public canary, although the policy does not require denial")
	}
	result.Violations = violations
	result.Notes = notes
	return result
}

// probeSandboxEgress runs the probe inside a live sandbox and evaluates it.
func (a *Activities) probeSandboxEgress(ctx context.Context, sb sandbox.Sandbox, policy string, expectDeny bool) (EgressProbeResult, error) {
	cfg := a.cfg.Hardening
	script, err := renderEgressProbeScript(cfg.CanaryURL(), cfg.MetadataURL())
	if err != nil {
		return EgressProbeResult{}, err
	}
	started := time.Now().UTC()
	exec, err := sb.Execute(ctx, sandbox.Command{
		Script:      script,
		Timeout:     cfg.EgressProbeTimeout(),
		Description: "factory egress and sandbox posture probe",
	})
	if err != nil {
		return EgressProbeResult{}, fmt.Errorf("run egress probe: %w", err)
	}
	if exec.ExitCode != 0 {
		return EgressProbeResult{}, fmt.Errorf("egress probe exited %d: %s", exec.ExitCode, boundProbeOutput("", exec.Stderr))
	}
	result := parseEgressProbe(cfg.CanaryURL(), cfg.MetadataURL(), exec.Stdout)
	result.Policy = policy
	result.ExpectDeny = expectDeny
	result.ProbedAt = started
	result.Duration = exec.Duration
	result.Raw = boundProbeOutput(exec.Stdout, exec.Stderr)
	return evaluateEgressProbe(result, cfg.RequireNonRoot), nil
}

// boundProbeOutput keeps the diagnostic record small and free of stray control
// characters. The probe's own output is a handful of short lines.
func boundProbeOutput(stdout, stderr string) string {
	raw := strings.TrimSpace(stdout)
	if s := strings.TrimSpace(stderr); s != "" {
		raw = raw + "\n[stderr] " + s
	}
	const limit = 4096
	if len(raw) > limit {
		raw = raw[:limit] + "...(truncated)"
	}
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 {
			return ' '
		}
		return r
	}, raw)
}
