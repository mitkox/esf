package factory

import (
	"sort"
	"time"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
)

// ThreatReport is a cross-run view of the hardening evidence.
//
// The factory is deliberately per-run: one task, one sandbox, one patch. That
// isolation is a strength, but it also means no single run's evidence can show
// a pattern. Twenty runs probing the same new destination, or one rule firing
// across a harness, is invisible from any one manifest. This report is the
// aggregate view, computed on demand from the durable manifests so it needs no
// extra write path and cannot drift from them.
type ThreatReport struct {
	// Runs is the number of manifests inspected.
	Runs int `json:"runs"`
	// RunsWithFindings counts runs whose behavior scan produced findings.
	RunsWithFindings int `json:"runs_with_findings"`
	// Tripped counts runs quarantined by the behavior monitor.
	Tripped int `json:"tripped"`
	// Blocked counts runs where the agent used the documented stop signal.
	Blocked int `json:"blocked"`
	// GateTampered counts runs whose patch modified its own gates.
	GateTampered int `json:"gate_tampered"`
	// EgressViolations counts runs whose sandbox boundary was proved violated.
	EgressViolations int `json:"egress_violations"`
	// AlertsUndelivered counts alerts that were recorded but not delivered.
	AlertsUndelivered int `json:"alerts_undelivered"`
	// Rules aggregates findings by rule ID, most frequent first.
	Rules []RuleTally `json:"rules,omitempty"`
	// EgressObservations aggregates the measured sandbox posture.
	EgressObservations []EgressObservation `json:"egress_observations,omitempty"`
	// Runs lists the per-run entries, newest first, capped by the caller.
	Entries []ThreatEntry `json:"entries,omitempty"`
}

// RuleTally is one rule's frequency across runs.
type RuleTally struct {
	RuleID   string `json:"rule_id"`
	Category string `json:"category,omitempty"`
	Severity string `json:"severity,omitempty"`
	Runs     int    `json:"runs"`
	Findings int    `json:"findings"`
}

// EgressObservation is one run's measured network posture.
type EgressObservation struct {
	RunID             string `json:"run_id"`
	Policy            string `json:"policy,omitempty"`
	ExpectDeny        bool   `json:"expect_deny"`
	CanaryReachable   bool   `json:"canary_reachable"`
	CanaryDecided     bool   `json:"canary_decided"`
	MetadataReachable bool   `json:"metadata_reachable"`
	MetadataDecided   bool   `json:"metadata_decided"`
	AgentUID          int    `json:"agent_uid"`
	Violations        int    `json:"violations"`
}

// ThreatEntry is the per-run summary an operator triages from.
type ThreatEntry struct {
	RunID        string    `json:"run_id"`
	Result       RunState  `json:"result"`
	Findings     int       `json:"findings"`
	MaxSeverity  string    `json:"max_severity,omitempty"`
	Tripped      bool      `json:"tripped"`
	Blocked      bool      `json:"blocked"`
	GateModified []string  `json:"gate_modified,omitempty"`
	EgressIssues int       `json:"egress_issues"`
	FinishedAt   time.Time `json:"finished_at"`
}

// BuildThreatReport aggregates hardening evidence across every run in a store.
//
// limit caps the number of per-run entries (0 means all); the aggregate counts
// always cover every inspected manifest, so a large deployment does not need to
// page through everything to see a pattern.
func BuildThreatReport(factory artifacts.Factory, limit int) (ThreatReport, error) {
	ids, err := ListRunIDs(factory)
	if err != nil {
		return ThreatReport{}, err
	}
	report := ThreatReport{}
	tallies := map[string]*RuleTally{}
	var observations []EgressObservation

	for _, runID := range ids {
		manifest, err := ReadManifest(factory, runID)
		if err != nil {
			// A manifest that cannot be read is not a threat signal; the run
			// may be in flight or its store may be mid-write. It is skipped
			// rather than failing the whole report.
			continue
		}
		report.Runs++
		entry := ThreatEntry{
			RunID:      manifest.RunID,
			Result:     manifest.FactoryResult,
			FinishedAt: manifest.CompletedAt,
		}
		if entry.RunID == "" {
			entry.RunID = runID
		}
		if h := manifest.Hardening; h != nil {
			if h.Behavior != nil {
				entry.Findings = len(h.Behavior.Findings)
				entry.MaxSeverity = string(h.Behavior.MaxSeverity)
				entry.Tripped = h.Behavior.Tripped
				if len(h.Behavior.Findings) > 0 {
					report.RunsWithFindings++
				}
				if h.Behavior.Tripped {
					report.Tripped++
				}
				seen := map[string]bool{}
				for _, finding := range h.Behavior.Findings {
					tally, ok := tallies[finding.RuleID]
					if !ok {
						tally = &RuleTally{RuleID: finding.RuleID, Category: finding.Category, Severity: string(finding.Severity)}
						tallies[finding.RuleID] = tally
					}
					tally.Findings++
					if !seen[finding.RuleID] {
						seen[finding.RuleID] = true
						tally.Runs++
					}
				}
			}
			if h.GateIntegrity != nil && len(h.GateIntegrity.Modified) > 0 {
				entry.GateModified = h.GateIntegrity.Modified
				report.GateTampered++
			}
			if h.EgressProbe != nil {
				entry.EgressIssues = len(h.EgressProbe.Violations)
				if len(h.EgressProbe.Violations) > 0 {
					report.EgressViolations++
				}
				observations = append(observations, EgressObservation{
					RunID:             entry.RunID,
					Policy:            h.EgressProbe.Policy,
					ExpectDeny:        h.EgressProbe.ExpectDeny,
					CanaryReachable:   h.EgressProbe.Canary.Reachable,
					CanaryDecided:     h.EgressProbe.Canary.Decided,
					MetadataReachable: h.EgressProbe.Metadata.Reachable,
					MetadataDecided:   h.EgressProbe.Metadata.Decided,
					AgentUID:          h.EgressProbe.AgentUID,
					Violations:        len(h.EgressProbe.Violations),
				})
			}
			for _, alert := range h.Alerts {
				if !alert.Delivered {
					report.AlertsUndelivered++
				}
			}
		}
		if manifest.FactoryResult == StateBlocked {
			entry.Blocked = true
			report.Blocked++
		}
		report.Entries = append(report.Entries, entry)
	}

	report.Rules = make([]RuleTally, 0, len(tallies))
	for _, tally := range tallies {
		report.Rules = append(report.Rules, *tally)
	}
	sort.Slice(report.Rules, func(i, j int) bool {
		if report.Rules[i].Findings == report.Rules[j].Findings {
			return report.Rules[i].RuleID < report.Rules[j].RuleID
		}
		return report.Rules[i].Findings > report.Rules[j].Findings
	})
	sort.Slice(observations, func(i, j int) bool { return observations[i].RunID < observations[j].RunID })
	report.EgressObservations = observations

	sort.Slice(report.Entries, func(i, j int) bool {
		if report.Entries[i].FinishedAt.Equal(report.Entries[j].FinishedAt) {
			return report.Entries[i].RunID < report.Entries[j].RunID
		}
		return report.Entries[i].FinishedAt.After(report.Entries[j].FinishedAt)
	})
	if limit > 0 && len(report.Entries) > limit {
		report.Entries = report.Entries[:limit]
	}
	return report, nil
}
