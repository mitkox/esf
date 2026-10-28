package console

import (
	"sort"

	"github.com/mitkox/esf/internal/factory"
)

func (s *Server) buildSecurity(spec windowSpec) SecurityResponse {
	orderedRuns, _, info := s.snapshot()
	response := SecurityResponse{
		Window:             spec.info(),
		GeneratedAt:        s.now().UTC(),
		Posture:            s.buildSecurityPosture(),
		Rules:              []factory.RuleTally{},
		EgressObservations: []factory.EgressObservation{},
		Entries:            []SecurityEntry{},
		Alerts:             []AlertSummary{},
		Stops:              []RunSummary{},
		Index:              info,
	}

	tallies := map[string]*factory.RuleTally{}
	var observations []factory.EgressObservation
	var entries []SecurityEntry
	var alerts []AlertSummary
	var stops []RunSummary

	for _, record := range orderedRuns {
		if !spec.contains(runWindowTime(record)) {
			continue
		}
		manifest := record.Manifest
		response.Inspected++
		entry := SecurityEntry{
			RunID:      record.ID,
			Result:     string(manifest.FactoryResult),
			Repository: manifest.Repository,
			Harness:    manifest.AgentHarness,
			Scope:      manifest.Scope,
			StartedAt:  manifest.StartedAt.UTC(),
			FinishedAt: manifest.CompletedAt.UTC(),
		}
		interesting := isSecurityState(manifest.FactoryResult) || manifest.FactoryResult == factory.StateBlocked
		if hardening := manifest.Hardening; hardening != nil {
			if hardening.Behavior != nil {
				entry.Findings = len(hardening.Behavior.Findings)
				entry.MaxSeverity = string(hardening.Behavior.MaxSeverity)
				entry.Tripped = hardening.Behavior.Tripped
				entry.Blocked = manifest.FactoryResult == factory.StateBlocked
				if entry.Findings > 0 {
					response.RunsWithFindings++
					interesting = true
				}
				if entry.Tripped {
					response.Tripped++
				}
				seen := map[string]bool{}
				for _, finding := range hardening.Behavior.Findings {
					tally, ok := tallies[finding.RuleID]
					if !ok {
						tally = &factory.RuleTally{RuleID: finding.RuleID, Category: finding.Category, Severity: string(finding.Severity)}
						tallies[finding.RuleID] = tally
					}
					tally.Findings++
					if !seen[finding.RuleID] {
						seen[finding.RuleID] = true
						tally.Runs++
					}
				}
			}
			if hardening.BehaviorError != "" {
				entry.EgressIssues++ // surfaces the scan failure as a triage flag
				interesting = true
			}
			if hardening.GateIntegrity != nil && len(hardening.GateIntegrity.Modified) > 0 {
				entry.GateModified = append([]string(nil), hardening.GateIntegrity.Modified...)
				response.GateTampered++
				interesting = true
			}
			if hardening.EgressProbe != nil {
				entry.EgressIssues += len(hardening.EgressProbe.Violations)
				if len(hardening.EgressProbe.Violations) > 0 {
					response.EgressViolations++
					interesting = true
				}
				observation := factory.EgressObservation{
					RunID:             record.ID,
					Policy:            hardening.EgressProbe.Policy,
					ExpectDeny:        hardening.EgressProbe.ExpectDeny,
					CanaryReachable:   hardening.EgressProbe.Canary.Reachable,
					CanaryDecided:     hardening.EgressProbe.Canary.Decided,
					MetadataReachable: hardening.EgressProbe.Metadata.Reachable,
					MetadataDecided:   hardening.EgressProbe.Metadata.Decided,
					AgentUID:          hardening.EgressProbe.AgentUID,
					Violations:        len(hardening.EgressProbe.Violations),
				}
				observations = append(observations, observation)
				response.Egress.Probed++
				if observation.CanaryReachable {
					response.Egress.CanaryReachable++
				}
				if observation.MetadataReachable {
					response.Egress.MetadataReach++
				}
				if observation.ExpectDeny {
					response.Egress.DeniedExpected++
				}
				if !observation.CanaryDecided || !observation.MetadataDecided {
					response.Egress.Inconclusive++
				}
				response.Egress.Violations += observation.Violations
			}
			for _, alert := range hardening.Alerts {
				alerts = append(alerts, AlertSummary{
					RunID: record.ID, Kind: alert.Kind, Severity: alert.Severity,
					Summary: alert.Summary, Delivered: alert.Delivered, Error: alert.Error, At: alert.At.UTC(),
				})
				if !alert.Delivered {
					response.AlertsUndelivered++
				}
			}
		}
		if manifest.FactoryResult == factory.StateBlocked {
			entry.Blocked = true
			response.Blocked++
			interesting = true
		}
		if isSecurityState(manifest.FactoryResult) {
			stops = append(stops, summarizeRun(record.ID, manifest))
		}
		if interesting {
			if len(entries) < 500 {
				entries = append(entries, entry)
			}
		}
	}

	rules := make([]factory.RuleTally, 0, len(tallies))
	for _, tally := range tallies {
		rules = append(rules, *tally)
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Findings == rules[j].Findings {
			return rules[i].RuleID < rules[j].RuleID
		}
		return rules[i].Findings > rules[j].Findings
	})

	sort.Slice(observations, func(i, j int) bool { return observations[i].RunID < observations[j].RunID })
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].FinishedAt.Equal(entries[j].FinishedAt) {
			return entries[i].RunID > entries[j].RunID
		}
		return entries[i].FinishedAt.After(entries[j].FinishedAt)
	})
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].At.After(alerts[j].At) })
	sort.Slice(stops, func(i, j int) bool {
		left, right := stops[i].CompletedAt, stops[j].CompletedAt
		if left == nil || right == nil {
			return left != nil
		}
		return left.After(*right)
	})

	if len(entries) > 100 {
		entries = entries[:100]
	}
	if len(observations) > 100 {
		observations = observations[:100]
	}
	if len(alerts) > 50 {
		alerts = alerts[:50]
	}
	if len(stops) > 20 {
		stops = stops[:20]
	}
	response.Rules = rules
	response.EgressObservations = observations
	response.Entries = entries
	response.Alerts = alerts
	response.Stops = stops
	if response.Rules == nil {
		response.Rules = []factory.RuleTally{}
	}
	if response.EgressObservations == nil {
		response.EgressObservations = []factory.EgressObservation{}
	}
	if response.Entries == nil {
		response.Entries = []SecurityEntry{}
	}
	if response.Alerts == nil {
		response.Alerts = []AlertSummary{}
	}
	if response.Stops == nil {
		response.Stops = []RunSummary{}
	}
	return response
}
