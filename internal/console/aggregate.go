package console

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/factory"
)

func (s *Server) buildOverview(ctx context.Context, spec windowSpec) Overview {
	orderedRuns, orderedChanges, info := s.snapshot()
	live := s.live.openRuns(ctx)
	probes := s.reachabilityProbes(ctx)

	windowRuns := make([]*runRecord, 0, len(orderedRuns))
	for _, record := range orderedRuns {
		if spec.contains(runWindowTime(record)) {
			windowRuns = append(windowRuns, record)
		}
	}
	previous := previousWindow(spec)
	previousRuns := make([]*runRecord, 0, len(orderedRuns))
	if previous.HasStart {
		for _, record := range orderedRuns {
			if previous.contains(runWindowTime(record)) {
				previousRuns = append(previousRuns, record)
			}
		}
	}

	overview := Overview{
		Window:       spec.info(),
		GeneratedAt:  s.now().UTC(),
		Index:        info,
		KPIs:         s.overviewKPIs(windowRuns, previousRuns, orderedChanges, live),
		Series:       buildSeries(windowRuns, spec),
		Outcomes:     buildOutcomes(windowRuns),
		Harnesses:    buildBreakdown(windowRuns, func(record *runRecord) string { return record.Manifest.AgentHarness }),
		Repositories: buildBreakdown(windowRuns, func(record *runRecord) string { return record.Manifest.Repository }),
		Scopes:       buildBreakdown(windowRuns, func(record *runRecord) string { return record.Manifest.Scope }),
		RecentRuns:   recentRunSummaries(orderedRuns, 8),
		Security:     buildSecuritySummary(windowRuns, s.index.threatReport()),
		Health:       s.buildHealth(info, live, probes, windowRuns, orderedChanges),
		Catalog: CatalogCounts{
			Harnesses:      len(s.cfg.Factory.Harnesses),
			Repositories:   len(s.cfg.Factory.Repositories.Allowed),
			Verification:   len(s.cfg.Factory.Verification),
			Budgets:        len(s.cfg.Factory.Budgets),
			Scopes:         len(s.cfg.Factory.Scopes),
			EgressPolicies: len(s.cfg.Factory.Egress),
		},
	}
	overview.Attention = s.buildAttention(windowRuns, orderedChanges, live)
	return overview
}

func runWindowTime(record *runRecord) time.Time {
	if !record.Manifest.StartedAt.IsZero() {
		return record.Manifest.StartedAt
	}
	return record.Manifest.CompletedAt
}

func (s *Server) overviewKPIs(windowRuns, previousRuns []*runRecord, changes []*changeRecord, live liveSnapshot) OverviewKPIs {
	current := computeRunKPIs(windowRuns)
	before := computeRunKPIs(previousRuns)

	kpis := OverviewKPIs{}
	kpis.Runs = KPI{Value: float64(current.total), Previous: float64(before.total), DeltaPct: deltaPercent(float64(current.total), float64(before.total)), Sample: current.total, Available: true}
	kpis.SuccessRate = KPI{Value: current.successRate, Previous: before.successRate, DeltaPct: deltaPercent(current.successRate, before.successRate), Sample: current.finished, Available: current.finished > 0}
	if current.finished == 0 {
		kpis.SuccessRate.Note = "No finished runs in this window."
	}
	kpis.SecurityStops = KPI{Value: float64(current.securityStops), Previous: float64(before.securityStops), DeltaPct: deltaPercent(float64(current.securityStops), float64(before.securityStops)), Sample: current.total, Available: true}
	kpis.CleanupVerifiedRate = KPI{Value: current.cleanupRate, Previous: before.cleanupRate, DeltaPct: deltaPercent(current.cleanupRate, before.cleanupRate), Sample: current.cleanupAttempted, Available: current.cleanupAttempted > 0}
	if current.cleanupAttempted == 0 {
		kpis.CleanupVerifiedRate.Note = "No cleanup evidence in this window."
	}
	kpis.CostUSD = KPI{Value: current.cost, Previous: before.cost, DeltaPct: deltaPercent(current.cost, before.cost), Sample: current.costSamples, Available: current.costSamples > 0}
	if current.costSamples == 0 {
		kpis.CostUSD.Note = "No harness reported inference cost."
	}
	kpis.Tokens = KPI{Value: float64(current.tokens), Previous: float64(before.tokens), DeltaPct: deltaPercent(float64(current.tokens), float64(before.tokens)), Sample: current.tokenSamples, Available: current.tokenSamples > 0}
	if current.tokenSamples == 0 {
		kpis.Tokens.Note = "No harness reported token usage."
	}
	kpis.P50DurationMS = KPI{Value: float64(current.p50Duration), Previous: float64(before.p50Duration), DeltaPct: deltaPercent(float64(current.p50Duration), float64(before.p50Duration)), Sample: current.durationSamples, Available: current.durationSamples > 0}
	kpis.P95DurationMS = KPI{Value: float64(current.p95Duration), Previous: float64(before.p95Duration), DeltaPct: deltaPercent(float64(current.p95Duration), float64(before.p95Duration)), Sample: current.durationSamples, Available: current.durationSamples > 0}

	pausedLive := 0
	for _, run := range live.Runs {
		if run.State == string(factory.StatePaused) {
			pausedLive++
		}
	}
	kpis.InFlight = InFlightKPI{Value: len(live.Runs), Paused: pausedLive, Available: live.Available}
	if !live.Available {
		kpis.InFlight.Note = live.Error
	}

	pending := 0
	var oldestWait time.Duration
	now := s.now()
	for _, record := range changes {
		if record.Change.Status != factory.ChangeInReview {
			continue
		}
		pending++
		wait := now.Sub(record.Change.UpdatedAt)
		if wait > oldestWait {
			oldestWait = wait
		}
	}
	kpis.Reviews = ReviewsKPI{Value: pending, OldestWaitMS: millis(oldestWait), PausedLive: pausedLive, Note: "All retained changes · not filtered by the window"}
	return kpis
}

type runAggregate struct {
	total            int
	finished         int
	succeeded        int
	successRate      float64
	securityStops    int
	cleanupAttempted int
	cleanupVerified  int
	cleanupRate      float64
	cost             float64
	costSamples      int
	tokens           int64
	tokenSamples     int
	durationSamples  int
	p50Duration      int64
	p95Duration      int64
}

func computeRunKPIs(runs []*runRecord) runAggregate {
	aggregate := runAggregate{total: len(runs)}
	var durations []int64
	for _, record := range runs {
		manifest := record.Manifest
		state := manifest.FactoryResult
		if isInflightState(state) {
			continue
		}
		aggregate.finished++
		if state == factory.StateSucceeded {
			aggregate.succeeded++
		}
		if isSecurityState(state) {
			aggregate.securityStops++
		}
		if manifest.CleanupResult.Attempted || manifest.CleanupResult.Outcome != "" {
			aggregate.cleanupAttempted++
			if manifest.CleanupResult.Verified {
				aggregate.cleanupVerified++
			}
		}
		if manifest.InferenceCost != nil {
			aggregate.cost += *manifest.InferenceCost
			aggregate.costSamples++
		}
		tokens := int64(0)
		reported := false
		if manifest.TokensIn != nil {
			tokens += *manifest.TokensIn
			reported = true
		}
		if manifest.TokensOut != nil {
			tokens += *manifest.TokensOut
			reported = true
		}
		if reported {
			aggregate.tokens += tokens
			aggregate.tokenSamples++
		}
		if duration := summaryDuration(manifest); duration > 0 {
			durations = append(durations, duration)
		}
	}
	aggregate.successRate = safeRatio(aggregate.succeeded, aggregate.finished)
	aggregate.cleanupRate = safeRatio(aggregate.cleanupVerified, aggregate.cleanupAttempted)
	aggregate.durationSamples = len(durations)
	if len(durations) > 0 {
		aggregate.p50Duration = medianDuration(durations)
		if p95 := percentileDuration(durations, 0.95); p95 > aggregate.p50Duration {
			aggregate.p95Duration = p95
		} else {
			aggregate.p95Duration = aggregate.p50Duration
		}
	}
	return aggregate
}

func summaryDuration(manifest factory.RunManifest) int64 {
	if manifest.Duration > 0 {
		return millis(manifest.Duration)
	}
	if !manifest.StartedAt.IsZero() && !manifest.CompletedAt.IsZero() {
		return millis(manifest.CompletedAt.Sub(manifest.StartedAt))
	}
	return 0
}

func medianDuration(values []int64) int64 {
	return percentileDuration(values, 0.5)
}

func percentileDuration(values []int64, percentile float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(float64(len(sorted)-1) * percentile)
	return sorted[index]
}

func buildSeries(runs []*runRecord, spec windowSpec) Series {
	granularity := "day"
	switch spec.Key {
	case "24h":
		granularity = "hour"
	case "all":
		granularity = "month"
	}
	buckets := map[string]*SeriesPoint{}
	durations := map[string][]int64{}
	order := []string{}

	keyFor := func(t time.Time) (string, string) {
		t = t.UTC()
		switch granularity {
		case "hour":
			key := t.Format("2006-01-02T15")
			return key, t.Format("15:04")
		case "month":
			key := t.Format("2006-01")
			return key, t.Format("Jan 2006")
		default:
			key := t.Format("2006-01-02")
			return key, t.Format("Jan 2")
		}
	}

	// Pre-seed the buckets for bounded windows so a quiet day is visible as a
	// zero rather than a missing point.
	if spec.HasStart && granularity != "month" {
		step := 24 * time.Hour
		if granularity == "hour" {
			step = time.Hour
		}
		for cursor := spec.Start.UTC(); !cursor.After(spec.End.UTC()); cursor = cursor.Add(step) {
			key, label := keyFor(cursor)
			if _, ok := buckets[key]; ok {
				continue
			}
			buckets[key] = &SeriesPoint{Date: key, Label: label}
			order = append(order, key)
		}
	}

	for _, record := range runs {
		t := runWindowTime(record)
		if t.IsZero() {
			continue
		}
		key, label := keyFor(t)
		point, ok := buckets[key]
		if !ok {
			point = &SeriesPoint{Date: key, Label: label}
			buckets[key] = point
			order = append(order, key)
		}
		point.Total++
		switch stateCategory(record.Manifest.FactoryResult) {
		case "succeeded":
			point.Succeeded++
		case "failed":
			point.Failed++
		case "security":
			point.Security++
		default:
			point.Other++
		}
		if record.Manifest.InferenceCost != nil {
			point.CostUSD += *record.Manifest.InferenceCost
		}
		if duration := summaryDuration(record.Manifest); duration > 0 {
			durations[key] = append(durations[key], duration)
		}
	}

	sort.Strings(order)
	points := make([]SeriesPoint, 0, len(order))
	for _, key := range order {
		point := buckets[key]
		if values := durations[key]; len(values) > 0 {
			point.P50DurationMS = medianDuration(values)
		}
		points = append(points, *point)
	}
	if len(points) > 120 && granularity == "day" {
		points = points[len(points)-120:]
	}
	if points == nil {
		points = []SeriesPoint{}
	}
	return Series{Granularity: granularity, Points: points}
}

func buildOutcomes(runs []*runRecord) []CountShare {
	counts := map[string]int{}
	total := 0
	for _, record := range runs {
		state := string(record.Manifest.FactoryResult)
		if state == "" {
			continue
		}
		counts[state]++
		total++
	}
	outcomes := make([]CountShare, 0, len(counts))
	for state, count := range counts {
		outcomes = append(outcomes, CountShare{Value: state, Count: count, Share: safeRatio(count, total)})
	}
	sort.Slice(outcomes, func(i, j int) bool {
		if outcomes[i].Count == outcomes[j].Count {
			return outcomes[i].Value < outcomes[j].Value
		}
		return outcomes[i].Count > outcomes[j].Count
	})
	return outcomes
}

func buildBreakdown(runs []*runRecord, key func(*runRecord) string) []Breakdown {
	type accumulator struct {
		runs      int
		finished  int
		success   int
		durations []int64
		cost      float64
	}
	values := map[string]*accumulator{}
	for _, record := range runs {
		name := strings.TrimSpace(key(record))
		if name == "" {
			name = "unassigned"
		}
		entry, ok := values[name]
		if !ok {
			entry = &accumulator{}
			values[name] = entry
		}
		entry.runs++
		state := record.Manifest.FactoryResult
		if !isInflightState(state) {
			entry.finished++
			if state == factory.StateSucceeded {
				entry.success++
			}
			if duration := summaryDuration(record.Manifest); duration > 0 {
				entry.durations = append(entry.durations, duration)
			}
		}
		if record.Manifest.InferenceCost != nil {
			entry.cost += *record.Manifest.InferenceCost
		}
	}
	breakdowns := make([]Breakdown, 0, len(values))
	for name, entry := range values {
		item := Breakdown{Name: name, Runs: entry.runs, SuccessRate: safeRatio(entry.success, entry.finished), CostUSD: entry.cost}
		if len(entry.durations) > 0 {
			item.P50DurationMS = medianDuration(entry.durations)
		}
		breakdowns = append(breakdowns, item)
	}
	sort.Slice(breakdowns, func(i, j int) bool {
		if breakdowns[i].Runs == breakdowns[j].Runs {
			return breakdowns[i].Name < breakdowns[j].Name
		}
		return breakdowns[i].Runs > breakdowns[j].Runs
	})
	if len(breakdowns) > 8 {
		breakdowns = breakdowns[:8]
	}
	return breakdowns
}

func recentRunSummaries(records []*runRecord, limit int) []RunSummary {
	if limit <= 0 || limit > len(records) {
		limit = len(records)
	}
	summaries := make([]RunSummary, 0, limit)
	for _, record := range records[:limit] {
		summaries = append(summaries, summarizeRun(record.ID, record.Manifest))
	}
	return summaries
}

func buildSecuritySummary(windowRuns []*runRecord, report factory.ThreatReport) SecuritySummary {
	summary := SecuritySummary{
		RunsScanned:       report.Runs,
		RunsWithFindings:  report.RunsWithFindings,
		Tripped:           report.Tripped,
		Blocked:           report.Blocked,
		GateTampered:      report.GateTampered,
		EgressViolations:  report.EgressViolations,
		AlertsUndelivered: report.AlertsUndelivered,
	}
	for _, record := range windowRuns {
		if isSecurityState(record.Manifest.FactoryResult) {
			summary.StopsInWindow++
		}
	}
	if len(report.Rules) > 4 {
		summary.TopRules = report.Rules[:4]
	} else {
		summary.TopRules = report.Rules
	}
	return summary
}

func (s *Server) buildHealth(info IndexInfo, live liveSnapshot, probes []Probe, windowRuns []*runRecord, changes []*changeRecord) HealthSummary {
	health := HealthSummary{Probes: probes, Index: info}
	checks := []HealthCheck{}

	storageState := "ok"
	storageDetail := fmt.Sprintf("%d runs · %d changes", info.Runs, info.Changes)
	if stat, err := os.Stat(s.root); err != nil || !stat.IsDir() {
		storageState = "error"
		storageDetail = "Evidence store is not readable."
	} else if info.Runs == 0 {
		storageState = "warn"
		storageDetail = "No run evidence found yet."
	}
	checks = append(checks, HealthCheck{ID: "storage", Label: "Evidence store", State: storageState, Detail: storageDetail})

	indexState := "ok"
	indexDetail := "Index is current."
	if info.Stale {
		indexState = "warn"
		indexDetail = "Showing the last complete index: " + info.ScanError
	} else if info.ScanErrors > 0 {
		indexState = "warn"
		indexDetail = fmt.Sprintf("%d evidence files could not be read.", info.ScanErrors)
	} else if info.RefreshedAt != nil {
		indexDetail = fmt.Sprintf("Refreshed %s ago", shortAge(s.now().Sub(*info.RefreshedAt)))
	}
	checks = append(checks, HealthCheck{ID: "index", Label: "Evidence index", State: indexState, Detail: indexDetail, LatencyMS: info.ScanMS})

	liveState := "disabled"
	liveDetail := "Live orchestration is disabled for this console."
	if s.cfg.Live {
		if live.Available {
			liveState = "ok"
			liveDetail = fmt.Sprintf("%d open run(s)", len(live.Runs))
		} else {
			liveState = "warn"
			liveDetail = live.Error
		}
	}
	checks = append(checks, HealthCheck{ID: "orchestration", Label: "Orchestration", State: liveState, Detail: liveDetail})

	cleanupState := "ok"
	cleanupDetail := "No sandbox cleanup anomaly in this window."
	failures := 0
	for _, record := range windowRuns {
		cleanup := record.Manifest.CleanupResult
		if cleanup.Attempted && (!cleanup.Verified || cleanup.Remaining > 0) {
			failures++
		}
	}
	if failures > 0 {
		cleanupState = "error"
		cleanupDetail = fmt.Sprintf("%d run(s) did not verify sandbox cleanup.", failures)
	}
	checks = append(checks, HealthCheck{ID: "cleanup", Label: "Sandbox cleanup", State: cleanupState, Detail: cleanupDetail})

	securityState := "ok"
	securityDetail := "No security stop in this window."
	stops := 0
	for _, record := range windowRuns {
		if isSecurityState(record.Manifest.FactoryResult) {
			stops++
		}
	}
	if stops > 0 {
		securityState = "error"
		securityDetail = fmt.Sprintf("%d security stop(s) in this window.", stops)
	}
	checks = append(checks, HealthCheck{ID: "security", Label: "Run security", State: securityState, Detail: securityDetail})

	pendingReviews := 0
	for _, change := range changes {
		if change.Change.Status == factory.ChangeInReview {
			pendingReviews++
		}
	}
	reviewState := "ok"
	reviewDetail := "No review is waiting."
	if pendingReviews > 0 {
		reviewState = "warn"
		reviewDetail = fmt.Sprintf("%d change(s) awaiting a human decision.", pendingReviews)
	}
	checks = append(checks, HealthCheck{ID: "reviews", Label: "Human review", State: reviewState, Detail: reviewDetail})

	status := "operational"
	for _, check := range checks {
		if check.State == "error" {
			status = "attention"
			break
		}
		if check.State == "warn" && status != "attention" {
			status = "degraded"
		}
	}
	for _, probe := range probes {
		if probe.State == "unreachable" {
			if status == "operational" {
				status = "degraded"
			}
		}
	}
	health.Status = status
	health.Checks = checks
	return health
}

func shortAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func (s *Server) buildAttention(windowRuns []*runRecord, changes []*changeRecord, live liveSnapshot) []AttentionItem {
	items := []AttentionItem{}
	add := func(item AttentionItem) { items = append(items, item) }

	for _, record := range windowRuns {
		manifest := record.Manifest
		runID := record.ID
		summary := summarizeRun(runID, manifest)
		switch {
		case isSecurityState(manifest.FactoryResult):
			add(AttentionItem{
				ID: "security:" + runID, Kind: "security_stop", Severity: "critical",
				Title: "Security stop: " + humanRunState(manifest.FactoryResult), Detail: manifest.Repository,
				RunID: runID, ChangeID: manifest.ChangeID, Repository: manifest.Repository,
				At: runEventTime(manifest), Href: detailHref("#/runs/", runID),
			})
		case isFailedState(manifest.FactoryResult):
			severity := "warning"
			if manifest.FactoryResult == factory.StateInfrastructureFailed || manifest.FactoryResult == factory.StateInvalidRequest {
				severity = "critical"
			}
			detail := manifest.Repository
			if summary.Error != "" {
				detail = summary.Error
			}
			add(AttentionItem{
				ID: "failed:" + runID, Kind: "failed_run", Severity: severity,
				Title: "Run " + humanRunState(manifest.FactoryResult), Detail: detail,
				RunID: runID, ChangeID: manifest.ChangeID, Repository: manifest.Repository,
				At: runEventTime(manifest), Href: detailHref("#/runs/", runID),
			})
		}
		if len(manifest.BudgetExceeded) > 0 {
			add(AttentionItem{
				ID: "budget:" + runID, Kind: "budget_exceeded", Severity: "warning",
				Title: "Budget ceiling crossed", Detail: strings.Join(manifest.BudgetExceeded, "; "),
				RunID: runID, ChangeID: manifest.ChangeID, Repository: manifest.Repository,
				At: runEventTime(manifest), Href: detailHref("#/runs/", runID),
			})
		}
		cleanup := manifest.CleanupResult
		if cleanup.Attempted && (!cleanup.Verified || cleanup.Remaining > 0) {
			detail := cleanup.Error
			if detail == "" {
				detail = fmt.Sprintf("%d sandbox(es) still present", cleanup.Remaining)
			}
			add(AttentionItem{
				ID: "cleanup:" + runID, Kind: "cleanup_failure", Severity: "critical",
				Title: "Sandbox cleanup did not verify", Detail: detail,
				RunID: runID, ChangeID: manifest.ChangeID, Repository: manifest.Repository,
				At: runEventTime(manifest), Href: "#/operations",
			})
		}
	}

	for _, record := range changes {
		if record.Change.Status != factory.ChangeInReview {
			continue
		}
		change := record.Change
		add(AttentionItem{
			ID: "review:" + change.ChangeID, Kind: "review_pending", Severity: "warning",
			Title: "Change awaiting review", Detail: change.Repository + " · all retained changes (queue is not windowed)",
			ChangeID: change.ChangeID, Repository: change.Repository,
			At: change.UpdatedAt.UTC(), Href: "#/reviews",
		})
	}

	for _, run := range live.Runs {
		if run.State != string(factory.StatePaused) {
			continue
		}
		add(AttentionItem{
			ID: "paused:" + run.RunID, Kind: "paused_run", Severity: "warning",
			Title: "Run paused for human review", Detail: run.Repository,
			RunID: run.RunID, ChangeID: run.ChangeID, Repository: run.Repository,
			At: run.StartedAt, Href: "#/reviews",
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		left, right := severityRank(items[i].Severity), severityRank(items[j].Severity)
		if left == right {
			return items[i].At.After(items[j].At)
		}
		return left > right
	})
	if len(items) > 12 {
		items = items[:12]
	}
	return items
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

func runEventTime(manifest factory.RunManifest) time.Time {
	if !manifest.CompletedAt.IsZero() {
		return manifest.CompletedAt.UTC()
	}
	if !manifest.StartedAt.IsZero() {
		return manifest.StartedAt.UTC()
	}
	return time.Time{}
}

// snapshot refreshes the evidence index on the request path and returns the
// current records. A scan failure never fails the request: the operator sees
// stale-but-labelled data and the health panel reports the degradation.
func (s *Server) snapshot() ([]*runRecord, []*changeRecord, IndexInfo) {
	if err := s.index.refresh(false); err != nil {
		s.log.Warn("console: evidence refresh failed", "error", err)
	}
	return s.index.snapshot()
}

// detailHref builds a frontend hash route with the ID encoded as one path
// segment, matching the SPA's hrefFor/routeFromHash round-trip. Custom IDs can
// contain %, # or spaces, which must not change the route.
func detailHref(prefix, id string) string {
	return prefix + url.PathEscape(id)
}
