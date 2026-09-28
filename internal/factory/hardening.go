package factory

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/tomlx"
)

// HardeningConfig is the defense-in-depth policy for the factory.
//
// The defaults are deliberately fail-closed for the boundary that matters most:
// network egress. A deployment that lets a sandbox reach the public internet
// must say so explicitly, because that single capability is what turns a
// confused or misaligned agent into a third-party intrusion.
//
// This configuration exists because the incident it guards against did not
// begin with an exotic capability. It began with an agent that had a network
// path out of its sandbox and no independent control checking that the path was
// closed.
type HardeningConfig struct {
	// AcknowledgeOpenEgress must be true for the factory to start when a run's
	// effective egress policy does not explicitly deny public internet.
	//
	// It is an acknowledgement, not a certificate: setting it records that the
	// operator accepts the risk. It never disables the per-run egress probe.
	AcknowledgeOpenEgress bool `toml:"acknowledge_open_egress"`

	// EgressProbe verifies, inside each run's sandbox, that the effective
	// network policy matches the policy the factory believes it applied.
	// Nil (absent) means enabled. Set false only on a deployment whose Cube
	// data plane cannot run the probe.
	//
	// The probe is the reason this configuration is honest: prior to it, the
	// factory recorded the policy it REQUESTED, not the policy in EFFECT.
	EgressProbe *bool `toml:"egress_probe"`

	// EgressCanaryURL is the public endpoint probed to decide whether the
	// sandbox can reach the internet. It must be a stable, neutral host.
	EgressCanaryURL string `toml:"egress_canary_url"`

	// MetadataProbeURL is the cloud instance-metadata endpoint. Reaching it is
	// always a finding: it is a credential source that does not require a
	// third-party compromise.
	MetadataProbeURL string `toml:"metadata_probe_url"`

	// RequireNonRoot fails a run whose sandbox agent would execute as uid 0.
	// CubeSandbox templates decide the user; this setting makes an unexpected
	// root sandbox a visible failure instead of a quiet assumption.
	RequireNonRoot bool `toml:"require_non_root"`

	// BehaviorMonitor scans the agent's captured output for indicators of
	// out-of-bounds behavior (privilege escalation, credential access,
	// reconnaissance, reverse shells, exfiltration). Nil (absent) means
	// enabled.
	BehaviorMonitor *bool `toml:"behavior_monitor"`

	// TripSeverity is the severity at or above which the monitor quarantines a
	// run. Defaults to HIGH.
	TripSeverity string `toml:"trip_severity"`

	// TripRulesFile is an optional extra rules file (JSON array). Operator
	// rules are ADDED to the built-in ruleset and cannot remove it.
	TripRulesFile string `toml:"trip_rules_file"`

	// AllowGateSelfModification permits a patch to modify the build or test
	// scripts it is judged by. Default false: a gate an agent can edit is not a
	// gate, so a self-modifying patch is recorded and fails closed.
	AllowGateSelfModification bool `toml:"allow_gate_self_modification"`

	// AlertWebhookURL receives bounded security alerts. Empty disables alerts.
	AlertWebhookURL string `toml:"alert_webhook_url"`

	// AlertWebhookTokenFile holds the bearer token for the webhook. Like every
	// other credential it is a path, never a value, and the file must not be
	// group- or world-readable.
	AlertWebhookTokenFile string `toml:"alert_webhook_token_file"`

	// AlertTimeout bounds one delivery attempt. Defaults to five seconds.
	AlertTimeout tomlx.Duration `toml:"alert_timeout"`

	// AlertOnBlocked also alerts when an agent legitimately reports BLOCKED.
	// Nil (absent) means enabled: a blocked agent is a signal about task
	// quality, not a security event, but an operator usually wants to know.
	AlertOnBlocked *bool `toml:"alert_on_blocked"`
}

const (
	// DefaultEgressCanaryURL is a stable, neutral public endpoint. It is probed
	// without credentials and its response body is discarded.
	DefaultEgressCanaryURL = "https://example.com/"
	// DefaultMetadataProbeURL is the AWS-style instance metadata endpoint. The
	// same address is used by every major cloud provider.
	DefaultMetadataProbeURL = "http://169.254.169.254/latest/meta-data/"
	// DefaultTripSeverity quarantines on HIGH and CRITICAL findings.
	DefaultTripSeverity = "HIGH"
	// DefaultAlertTimeout bounds one webhook delivery.
	DefaultAlertTimeout = 5 * time.Second
	// defaultEgressProbeTimeout bounds the whole probe command.
	defaultEgressProbeTimeout = 45 * time.Second
)

// EgressProbeEnabled reports whether the per-run egress probe runs.
func (h HardeningConfig) EgressProbeEnabled() bool { return h.EgressProbe == nil || *h.EgressProbe }

// BehaviorMonitorEnabled reports whether the behavior monitor runs.
func (h HardeningConfig) BehaviorMonitorEnabled() bool {
	return h.BehaviorMonitor == nil || *h.BehaviorMonitor
}

// AlertOnBlockedEnabled reports whether a blocked agent raises an alert.
func (h HardeningConfig) AlertOnBlockedEnabled() bool {
	return h.AlertOnBlocked == nil || *h.AlertOnBlocked
}

// CanaryURL returns the configured canary, or the default.
func (h HardeningConfig) CanaryURL() string {
	if v := strings.TrimSpace(h.EgressCanaryURL); v != "" {
		return v
	}
	return DefaultEgressCanaryURL
}

// MetadataURL returns the configured metadata endpoint, or the default.
func (h HardeningConfig) MetadataURL() string {
	if v := strings.TrimSpace(h.MetadataProbeURL); v != "" {
		return v
	}
	return DefaultMetadataProbeURL
}

// TripLevel returns the configured trip severity, upper-cased, defaulting to
// HIGH. Validation guarantees it is one of the four known levels.
func (h HardeningConfig) TripLevel() string {
	if v := strings.ToUpper(strings.TrimSpace(h.TripSeverity)); v != "" {
		return v
	}
	return DefaultTripSeverity
}

// AlertsEnabled reports whether a webhook is configured.
func (h HardeningConfig) AlertsEnabled() bool { return strings.TrimSpace(h.AlertWebhookURL) != "" }

// Timeout returns the alert delivery timeout, defaulting to five seconds.
func (h HardeningConfig) Timeout() time.Duration {
	if d := h.AlertTimeout.Std(); d > 0 {
		return d
	}
	return DefaultAlertTimeout
}

// EgressProbeTimeout returns the bound for the whole in-sandbox probe.
func (h HardeningConfig) EgressProbeTimeout() time.Duration { return defaultEgressProbeTimeout }

var validTripLevels = map[string]bool{
	"LOW": true, "MEDIUM": true, "HIGH": true, "CRITICAL": true,
}

// validate checks the shapes the factory depends on. It cannot check whether a
// webhook is reachable, and it deliberately does not read the token: the file
// is opened only at delivery time, inside the activity worker.
func (h HardeningConfig) validate() error {
	var problems []string
	if level := strings.ToUpper(strings.TrimSpace(h.TripSeverity)); level != "" && !validTripLevels[level] {
		problems = append(problems, fmt.Sprintf("trip_severity %q must be one of LOW, MEDIUM, HIGH, CRITICAL", h.TripSeverity))
	}
	if raw := strings.TrimSpace(h.EgressCanaryURL); raw != "" {
		if err := validateProbeURL(raw, true); err != nil {
			problems = append(problems, "egress_canary_url: "+err.Error())
		}
	}
	if raw := strings.TrimSpace(h.MetadataProbeURL); raw != "" {
		if err := validateProbeURL(raw, false); err != nil {
			problems = append(problems, "metadata_probe_url: "+err.Error())
		}
	}
	if h.AlertTimeout < 0 {
		problems = append(problems, "alert_timeout must not be negative")
	}
	if raw := strings.TrimSpace(h.AlertWebhookURL); raw != "" {
		if err := validateProbeURL(raw, false); err != nil {
			problems = append(problems, "alert_webhook_url: "+err.Error())
		}
	}
	if path := strings.TrimSpace(h.AlertWebhookTokenFile); path != "" {
		if !filepath.IsAbs(path) {
			problems = append(problems, "alert_webhook_token_file must be an absolute path")
		} else if info, err := os.Lstat(path); err != nil {
			problems = append(problems, fmt.Sprintf("alert_webhook_token_file: %v", err))
		} else if !info.Mode().IsRegular() {
			problems = append(problems, "alert_webhook_token_file must be a regular file, not a symlink or device")
		} else if info.Mode().Perm()&0o077 != 0 {
			problems = append(problems, fmt.Sprintf("alert_webhook_token_file permissions %04o expose it to group or other users", info.Mode().Perm()))
		}
	} else if h.AlertsEnabled() {
		// A webhook without a token is allowed: many receivers are authenticated
		// by an unguessable URL. This is a note, not a problem.
		_ = h
	}
	return problemListError(problems)
}

// validateProbeURL requires a parseable http(s) URL with a host and no
// embedded credentials. requireHTTPS is used for the egress canary because a
// plaintext canary cannot distinguish a transparent proxy from a real
// connection.
func validateProbeURL(raw string, requireHTTPS bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if requireHTTPS && u.Scheme != "https" {
		return fmt.Errorf("scheme must be https")
	}
	if u.Host == "" {
		return fmt.Errorf("host is required")
	}
	if u.User != nil {
		return fmt.Errorf("URL must not embed credentials")
	}
	return nil
}

// AllowsInternet reports whether the policy leaves public egress possible.
//
// A nil AllowInternet is the deployment default, which CubeSandbox documents as
// "public egress allowed, internal CIDRs denied". Only an explicit false denies.
func (e EgressPolicyConfig) AllowsInternet() bool {
	return e.AllowInternet == nil || *e.AllowInternet
}

// openEgressPolicies names every configured policy that does not explicitly
// deny public internet, in stable order.
func (c Config) openEgressPolicies() []string {
	names := make([]string, 0, len(c.Egress))
	for name, policy := range c.Egress {
		if policy.AllowsInternet() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// validateHardening enforces the fail-closed acknowledgement.
//
// The rule is deliberately blunt: if ANY configured egress policy permits the
// public internet, or if no policy is configured at all (so sandboxes inherit
// the deployment default), the operator must set acknowledge_open_egress. The
// factory would rather refuse to start than silently run agents with an open
// network boundary.
func (c Config) validateHardening() error {
	var problems []string
	if err := c.Hardening.validate(); err != nil {
		problems = append(problems, "hardening: "+err.Error())
	}
	if !c.Hardening.AcknowledgeOpenEgress {
		if names := c.openEgressPolicies(); len(names) > 0 {
			problems = append(problems, fmt.Sprintf(
				"hardening: egress policies [%s] do not set allow_internet = false, so sandboxes may reach the public internet; "+
					"set allow_internet = false on those policies, or set hardening.acknowledge_open_egress = true to accept the risk explicitly",
				strings.Join(names, ", ")))
		} else if len(c.Egress) == 0 {
			problems = append(problems, "hardening: no [egress] policy is configured, so every sandbox inherits the deployment default "+
				"(public egress allowed); define [egress.<name>] with allow_internet = false and reference it from a scope, "+
				"or set hardening.acknowledge_open_egress = true to accept the risk explicitly")
		}
	}
	return problemListError(problems)
}

// HardeningWarnings returns operator-visible risks that are not fatal.
//
// They are warnings rather than errors because each one is a legitimate
// configuration in some deployment; together they are the difference between a
// hardened factory and a factory that merely looks hardened. `factory doctor`
// prints them.
func (c Config) HardeningWarnings() []string {
	var warnings []string
	if !c.Hardening.EgressProbeEnabled() {
		warnings = append(warnings, "hardening.egress_probe is disabled: the factory records the egress policy it requested, not the policy in effect")
	}
	if c.Hardening.AcknowledgeOpenEgress {
		warnings = append(warnings, "hardening.acknowledge_open_egress is true: at least one egress policy permits the public internet")
	}
	if !c.Hardening.BehaviorMonitorEnabled() {
		warnings = append(warnings, "hardening.behavior_monitor is disabled: out-of-bounds agent behavior will not be detected or quarantined")
	}
	if !c.Hardening.AlertsEnabled() {
		warnings = append(warnings, "no hardening.alert_webhook_url is configured: security alerts are recorded in evidence but never delivered")
	}
	legacy := make([]string, 0)
	for name, h := range c.Harnesses {
		mode := strings.ToLower(strings.TrimSpace(h.CredentialMode))
		if mode != "" && mode != "cube_egress" {
			legacy = append(legacy, name+" (credential_mode="+mode+")")
		}
		if len(h.ProviderFiles) > 0 {
			legacy = append(legacy, name+" (provider_files)")
		}
		if len(h.PassEnv) > 0 {
			legacy = append(legacy, name+" (pass_env)")
		}
	}
	if len(legacy) > 0 {
		sort.Strings(legacy)
		warnings = append(warnings, "harnesses expose credentials inside the sandbox instead of using cube_egress: "+strings.Join(legacy, ", "))
	}
	if !c.Hardening.RequireNonRoot {
		warnings = append(warnings, "hardening.require_non_root is false: a template that runs the agent as uid 0 is accepted")
	}
	if len(c.Verification) > 0 && !c.Hardening.AllowGateSelfModification {
		// This is the hardened default; no warning needed. The branch exists so
		// the intent is visible next to its opposite.
		_ = c
	}
	return warnings
}

// GateScriptPaths returns the repository-relative paths of every mandatory
// verification gate program across all profiles, in stable order.
//
// This is the list a patch must not modify unless the operator has explicitly
// allowed gate self-modification. Only the first argv element is considered:
// a gate that shells out to a helper it trusts is the operator's business.
func (c Config) GateScriptPaths() []string {
	seen := map[string]bool{}
	paths := make([]string, 0)
	for _, profile := range c.Verification {
		for _, step := range profile.Steps {
			if len(step.Argv) == 0 {
				continue
			}
			p := normalizeGatePath(step.Argv[0])
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// normalizeGatePath reduces a gate program to a comparable repository-relative
// path. "./build.sh" and "build.sh" are the same file; an absolute path is
// returned unchanged because it is outside the repository by definition.
func normalizeGatePath(program string) string {
	p := strings.TrimSpace(program)
	if p == "" {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	if strings.HasPrefix(p, "/") {
		return p
	}
	return path.Clean(p)
}

// problemListError formats accumulated configuration problems, or nil.
func problemListError(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(problems, "; "))
}
