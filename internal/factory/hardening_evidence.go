package factory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/notify"
	"github.com/mitkox/esf/internal/threatmon"
)

// GateIntegrity records whether the agent's patch touched the deterministic
// gates that judge it.
//
// A gate the agent can edit is not a gate. This is the concrete form of the
// report's reward-hacking finding: the model that drove the incident learned to
// write into the environment where the reference answer lived.
type GateIntegrity struct {
	// Allowed is true when the operator permitted gate self-modification, so
	// nothing is treated as a violation.
	Allowed bool `json:"allowed"`
	// GatePaths are the repository-relative gate programs that were checked.
	GatePaths []string `json:"gate_paths,omitempty"`
	// Modified lists gate paths the agent changed.
	Modified []string `json:"modified,omitempty"`
	// Checked reports that the comparison actually ran. A check that did not run
	// is never reported as clean.
	Checked bool `json:"checked"`
	// Method names how the comparison was made, so an auditor can tell a
	// content comparison from a skipped one.
	Method string `json:"method,omitempty"`
}

// Gate check methods.
const (
	// GateMethodTreeHash compares working-tree content against baseline blobs.
	GateMethodTreeHash = "tree-hash"
	// GateMethodAllowed means the operator permitted self-modification.
	GateMethodAllowed = "allowed"
	// GateMethodNoGates means no repository-local gate program is configured.
	GateMethodNoGates = "no-gates"
)

// AlertRecord is the durable record of one security alert.
//
// Delivery is best-effort by design: a run must never fail because a webhook
// was unreachable. The record is what keeps that failure visible instead of
// silent.
type AlertRecord struct {
	Kind      string    `json:"kind"`
	Severity  string    `json:"severity"`
	Summary   string    `json:"summary"`
	Delivered bool      `json:"delivered"`
	Error     string    `json:"error,omitempty"`
	At        time.Time `json:"at"`
}

// Alert kinds. They are stable strings so an operator can filter on them.
const (
	AlertQuarantine     = "run.quarantined"
	AlertBlocked        = "run.blocked"
	AlertGateTampered   = "run.gate_tampered"
	AlertEgressViolated = "run.egress_violation"
	AlertSandboxLeak    = "run.sandbox_leak"
)

// newNotifier builds the configured notifier. An empty webhook URL yields a Nop
// notifier rather than an error: alerting is optional infrastructure.
func (c Config) newNotifier() (notify.Notifier, error) {
	if !c.Hardening.AlertsEnabled() {
		return notify.Nop{}, nil
	}
	token := ""
	if path := strings.TrimSpace(c.Hardening.AlertWebhookTokenFile); path != "" {
		value, err := readCredentialFile(path)
		if err != nil {
			return nil, fmt.Errorf("read alert webhook token: %w", err)
		}
		token = value
	}
	return notify.NewWebhook(notify.WebhookConfig{
		URL:     strings.TrimSpace(c.Hardening.AlertWebhookURL),
		Token:   token,
		Timeout: c.Hardening.Timeout(),
	})
}

// raiseAlert delivers one alert and returns its durable record.
//
// The returned error is only for a configuration problem that makes delivery
// impossible (an unreadable token file). A delivery failure is recorded in the
// AlertRecord, because a security control that fails a run when its pager is
// down would be turned off by the first operator who hit it.
func (a *Activities) raiseAlert(ctx context.Context, runID, kind string, severity notify.Severity, summary string, detail map[string]string) AlertRecord {
	record := AlertRecord{Kind: kind, Severity: string(severity), Summary: summary, At: time.Now().UTC()}
	if !a.cfg.Hardening.AlertsEnabled() {
		// "Delivered" must mean delivered. With no webhook configured the alert
		// exists only in evidence, and the record says so rather than reporting
		// a delivery that never happened.
		record.Error = "no hardening.alert_webhook_url is configured"
		a.log.WarnContext(ctx, "security alert recorded without delivery",
			"run.id", runID, "kind", kind, "severity", string(severity))
		return record
	}
	notifier, err := a.cfg.newNotifier()
	if err != nil {
		record.Error = a.redactor.Redact(err.Error())
		a.log.ErrorContext(ctx, "security alert could not be configured",
			"run.id", runID, "kind", kind, "error", record.Error)
		return record
	}
	event := notify.Event{
		Kind:     kind,
		Severity: severity,
		RunID:    runID,
		Summary:  a.redactor.Redact(summary),
		Detail:   map[string]string{},
		At:       record.At,
	}
	for key, value := range detail {
		event.Detail[key] = a.redactor.Redact(value)
	}
	notifyCtx, cancel := context.WithTimeout(ctx, a.cfg.Hardening.Timeout())
	defer cancel()
	if err := notifier.Notify(notifyCtx, event); err != nil {
		record.Error = a.redactor.Redact(err.Error())
		a.log.WarnContext(ctx, "security alert delivery failed",
			"run.id", runID, "kind", kind, "error", record.Error)
		return record
	}
	record.Delivered = true
	a.log.InfoContext(ctx, "security alert delivered", "run.id", runID, "kind", kind, "severity", string(severity))
	return record
}

// threatReportSummary renders a report for a condition message or an alert
// detail. It is deliberately terse: the full findings are evidence, not a log.
func threatReportSummary(report threatmon.Report) string {
	if len(report.Findings) == 0 {
		return "no findings"
	}
	ids := make([]string, 0, len(report.Findings))
	seen := map[string]bool{}
	for _, f := range report.Findings {
		if seen[f.RuleID] {
			continue
		}
		seen[f.RuleID] = true
		ids = append(ids, f.RuleID)
	}
	summary := fmt.Sprintf("%d finding(s), max severity %s: %s", len(report.Findings), report.MaxSeverity, strings.Join(ids, ", "))
	const limit = 480
	if len(summary) > limit {
		summary = summary[:limit] + "..."
	}
	return summary
}

// threatReportDetail flattens the first findings into alert detail. Alerts are
// bounded by the notifier, but keeping this small keeps them useful.
func threatReportDetail(report threatmon.Report) map[string]string {
	detail := map[string]string{
		"findings":     fmt.Sprintf("%d", len(report.Findings)),
		"max_severity": string(report.MaxSeverity),
		"ruleset":      report.RulesetVersion,
	}
	const maxFindings = 5
	for i, f := range report.Findings {
		if i >= maxFindings {
			break
		}
		detail[fmt.Sprintf("finding_%d", i+1)] = fmt.Sprintf("%s %s %s:%d", f.Severity, f.RuleID, f.Stream, f.Line)
	}
	return detail
}

// probeSummary renders an egress probe for a condition message.
func probeSummary(p EgressProbeResult) string {
	return fmt.Sprintf("expect_deny=%v internet=%s metadata=%s uid=%d", p.ExpectDeny,
		probeReachability(p.Canary), probeReachability(p.Metadata), p.AgentUID)
}

func probeReachability(p ProbeObservation) string {
	if !p.Decided {
		return "unknown"
	}
	if p.Reachable {
		return "reachable"
	}
	return "blocked"
}

// mergeThreatReports combines two scans into one deterministic report.
//
// The run scans the agent's output and the collected patch separately; the
// verdict must be a function of both. Ordering is stable so the merged report is
// identical under replay.
func mergeThreatReports(a, b *threatmon.Report) *threatmon.Report {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		return b
	case b == nil:
		return a
	}
	merged := *a
	merged.Findings = append(append([]threatmon.Finding(nil), a.Findings...), b.Findings...)
	merged.ScannedLines = a.ScannedLines + b.ScannedLines
	if b.MaxSeverity.Rank() > merged.MaxSeverity.Rank() {
		merged.MaxSeverity = b.MaxSeverity
	}
	merged.Tripped = a.Tripped || b.Tripped
	if merged.RulesetVersion == "" {
		merged.RulesetVersion = b.RulesetVersion
	}
	sort.SliceStable(merged.Findings, func(i, j int) bool {
		if merged.Findings[i].Stream != merged.Findings[j].Stream {
			return merged.Findings[i].Stream < merged.Findings[j].Stream
		}
		if merged.Findings[i].Line != merged.Findings[j].Line {
			return merged.Findings[i].Line < merged.Findings[j].Line
		}
		return merged.Findings[i].RuleID < merged.Findings[j].RuleID
	})
	return &merged
}
