package console

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/factory"
)

// ── Windows ─────────────────────────────────────────────────────────────────

type windowSpec struct {
	Key      string
	Label    string
	Start    time.Time
	HasStart bool
	End      time.Time
}

func parseWindow(raw string, now time.Time) windowSpec {
	spec := windowSpec{Key: strings.ToLower(strings.TrimSpace(raw)), End: now.UTC()}
	switch spec.Key {
	case "", "30d":
		spec.Key, spec.Label = "30d", "Last 30 days"
		spec.Start, spec.HasStart = now.AddDate(0, 0, -30), true
	case "24h":
		spec.Label = "Last 24 hours"
		spec.Start, spec.HasStart = now.Add(-24*time.Hour), true
	case "7d":
		spec.Label = "Last 7 days"
		spec.Start, spec.HasStart = now.AddDate(0, 0, -7), true
	case "90d":
		spec.Label = "Last 90 days"
		spec.Start, spec.HasStart = now.AddDate(0, 0, -90), true
	case "all":
		spec.Label = "All time"
	default:
		spec.Key, spec.Label = "30d", "Last 30 days"
		spec.Start, spec.HasStart = now.AddDate(0, 0, -30), true
	}
	return spec
}

func (w windowSpec) contains(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	if w.HasStart && t.Before(w.Start) {
		return false
	}
	return !t.After(w.End.Add(time.Minute))
}

func (w windowSpec) info() WindowInfo {
	info := WindowInfo{Key: w.Key, Label: w.Label, End: w.End}
	if w.HasStart {
		start := w.Start.UTC()
		info.Start = &start
	}
	return info
}

func previousWindow(spec windowSpec) windowSpec {
	if !spec.HasStart {
		return windowSpec{Key: spec.Key, Label: spec.Label}
	}
	length := spec.End.Sub(spec.Start)
	return windowSpec{Key: spec.Key, Label: spec.Label, Start: spec.Start.Add(-length), HasStart: true, End: spec.Start}
}

// ── State classification ────────────────────────────────────────────────────

func isInflightState(state factory.RunState) bool {
	switch state {
	case factory.StateRequested, factory.StateRunning, factory.StatePaused:
		return true
	default:
		return false
	}
}

func isSecurityState(state factory.RunState) bool { return state.IsSecurityTerminal() }

func isFailedState(state factory.RunState) bool {
	switch state {
	case factory.StateAgentFailed, factory.StateVerificationFailed, factory.StateInfrastructureFailed, factory.StateInvalidRequest, factory.StateRejected:
		return true
	default:
		return false
	}
}

func stateCategory(state factory.RunState) string {
	switch {
	case state == factory.StateSucceeded:
		return "succeeded"
	case isSecurityState(state):
		return "security"
	case isFailedState(state):
		return "failed"
	default:
		return "other"
	}
}

// ── Summaries ───────────────────────────────────────────────────────────────

func summarizeCleanup(cleanup factory.CleanupResult) CleanupSummary {
	errorText, _ := truncateText(cleanup.Error, maxErrorText)
	return CleanupSummary{
		Attempted: cleanup.Attempted,
		Outcome:   string(cleanup.Outcome),
		Verified:  cleanup.Verified,
		Remaining: cleanup.Remaining,
		Error:     errorText,
	}
}

func summarizeHardening(hardening *factory.HardeningEvidence) HardeningSummary {
	if hardening == nil {
		return HardeningSummary{}
	}
	summary := HardeningSummary{Measured: true}
	if hardening.Behavior != nil {
		summary.Findings = len(hardening.Behavior.Findings)
		summary.MaxSeverity = string(hardening.Behavior.MaxSeverity)
		summary.Tripped = hardening.Behavior.Tripped
	}
	if hardening.EgressProbe != nil {
		summary.EgressViolations = len(hardening.EgressProbe.Violations)
	}
	if hardening.GateIntegrity != nil {
		summary.GateChecked = hardening.GateIntegrity.Checked
		summary.GateAllowed = hardening.GateIntegrity.Allowed
		summary.GateModified = append([]string(nil), hardening.GateIntegrity.Modified...)
	}
	return summary
}

func summarizeIntake(intake *factory.IntakeResult) *IntakeSummary {
	if intake == nil {
		return nil
	}
	summary := &IntakeSummary{
		Status:     intake.Status,
		Model:      intake.Model,
		DurationMS: intake.DurationMS,
		ErrorCode:  intake.ErrorCode,
	}
	if intake.Ready != nil {
		value := intake.Ready.Value
		summary.Ready = &value
		summary.ReadyProb = intake.Ready.Probability
		summary.ReadyConf = intake.Ready.Confidence
	}
	if intake.TaskType != nil {
		summary.TaskType = intake.TaskType.Value
		summary.TaskTypeCf = intake.TaskType.Confidence
	}
	if intake.Ambiguity != nil {
		summary.Ambiguity = intake.Ambiguity.Level
		summary.AmbiguityCf = intake.Ambiguity.Confidence
	}
	return summary
}

func summarizeRun(id string, manifest factory.RunManifest) RunSummary {
	runID := manifest.RunID
	if runID == "" {
		runID = id
	}
	duration := manifest.Duration
	if duration <= 0 && !manifest.StartedAt.IsZero() && !manifest.CompletedAt.IsZero() {
		duration = manifest.CompletedAt.Sub(manifest.StartedAt)
	}
	errorText, _ := truncateText(manifest.Error, maxErrorText)
	reviewNote, _ := truncateText(manifest.ReviewNote, maxErrorText)
	return RunSummary{
		RunID:               runID,
		ChangeID:            manifest.ChangeID,
		ParentRunID:         manifest.ParentRunID,
		Scope:               manifest.Scope,
		Repository:          manifest.Repository,
		RepositoryKind:      manifest.RepositoryKind,
		RequestedRevision:   manifest.RequestedRevision,
		BaselineSHA:         manifest.BaselineSHA,
		ResultingSHA:        manifest.ResultingSHA,
		AgentHarness:        manifest.AgentHarness,
		AgentVersion:        manifest.AgentVersion,
		VerificationProfile: manifest.VerificationProfile,
		SandboxID:           manifest.SandboxID,
		FactoryResult:       string(manifest.FactoryResult),
		AgentResult:         string(manifest.AgentResult),
		VerificationResult:  string(manifest.VerificationResult),
		CleanupResult:       summarizeCleanup(manifest.CleanupResult),
		Hardening:           summarizeHardening(manifest.Hardening),
		StartedAt:           timePtr(manifest.StartedAt.UTC()),
		CompletedAt:         timePtr(manifest.CompletedAt.UTC()),
		DurationMS:          millis(duration),
		ReviewConfigured:    manifest.ReviewConfigured,
		ReviewOutcome:       manifest.ReviewOutcome,
		ReviewNote:          reviewNote,
		BudgetExceeded:      append([]string(nil), manifest.BudgetExceeded...),
		TokensIn:            manifest.TokensIn,
		TokensOut:           manifest.TokensOut,
		InferenceCost:       manifest.InferenceCost,
		Intake:              summarizeIntake(manifest.Intake),
		Error:               errorText,
	}
}

func summarizeActivation(activation factory.Activation) ActivationSummary {
	return ActivationSummary{
		RunID:       activation.RunID,
		ParentRunID: activation.ParentRunID,
		Reason:      activation.Reason,
		CreatedAt:   activation.CreatedAt.UTC(),
	}
}

func summarizeChange(change factory.Change) ChangeSummary {
	taskPreview, _ := truncateText(change.Task, 180)
	reworkNote, _ := truncateText(change.ReworkNote, maxErrorText)
	activations := make([]ActivationSummary, 0, len(change.Activations))
	for _, activation := range change.Activations {
		activations = append(activations, summarizeActivation(activation))
	}
	return ChangeSummary{
		ChangeID:          change.ChangeID,
		Scope:             change.Scope,
		Repository:        change.Repository,
		RequestedRevision: change.RequestedRevision,
		TaskPreview:       taskPreview,
		TaskHash:          change.TaskHash,
		Status:            string(change.Status),
		Attempts:          change.Attempts,
		TotalCostUSD:      change.TotalCostUSD,
		TotalTokens:       change.TotalTokens,
		LastRunID:         change.LastRunID,
		LastResult:        string(change.LastResult),
		CreatedAt:         change.CreatedAt.UTC(),
		UpdatedAt:         change.UpdatedAt.UTC(),
		ReworkNote:        reworkNote,
		Activations:       activations,
	}
}

// ── Actions (copyable CLI commands) ─────────────────────────────────────────

func runActions(runID string) RunActions {
	return RunActions{
		ApproveCommand: fmt.Sprintf("factory review %s --approve", runID),
		RejectCommand:  fmt.Sprintf("factory review %s --reject --note \"…\"", runID),
		CancelCommand:  fmt.Sprintf("factory cancel %s", runID),
		Describe:       fmt.Sprintf("factory describe runs %s", runID),
		Logs:           fmt.Sprintf("factory logs %s", runID),
	}
}

// ── Facets ──────────────────────────────────────────────────────────────────

func facetCounts(counts map[string]int) []Facet {
	facets := make([]Facet, 0, len(counts))
	for value, count := range counts {
		facets = append(facets, Facet{Value: value, Count: count})
	}
	sort.Slice(facets, func(i, j int) bool {
		if facets[i].Count == facets[j].Count {
			return facets[i].Value < facets[j].Value
		}
		return facets[i].Count > facets[j].Count
	})
	return facets
}

// ── Numbers ─────────────────────────────────────────────────────────────────

func safeRatio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func deltaPercent(current, previous float64) *float64 {
	if previous == 0 {
		return nil
	}
	value := (current - previous) / previous * 100
	return &value
}

func sumFloat(values []float64) float64 {
	var total float64
	for _, value := range values {
		total += value
	}
	return total
}

// humanRunState renders a run state for prose titles. The raw state remains in
// the JSON so clients can style it consistently.
func humanRunState(state factory.RunState) string {
	switch state {
	case factory.StateSucceeded:
		return "succeeded"
	case factory.StateVerificationFailed:
		return "failed verification"
	case factory.StateAgentFailed:
		return "agent failed"
	case factory.StateInfrastructureFailed:
		return "infrastructure failed"
	case factory.StateInvalidRequest:
		return "invalid request"
	case factory.StateCancelled:
		return "cancelled"
	case factory.StateBlocked:
		return "blocked by agent"
	case factory.StateQuarantined:
		return "quarantined"
	case factory.StateGateTampered:
		return "gate tampered"
	case factory.StateEgressUnverified:
		return "egress unverified"
	case factory.StateRejected:
		return "rejected in review"
	case factory.StatePaused:
		return "paused for review"
	case factory.StateRunning:
		return "running"
	case factory.StateRequested:
		return "requested"
	default:
		return strings.ToLower(string(state))
	}
}
