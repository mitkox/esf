package console

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/threatmon"
)

const testToken = "0123456789abcdef0123456789abcdef" // gitleaks:allow (fixed test credential)

type testClock struct {
	current time.Time
}

func (c *testClock) Now() time.Time { return c.current }

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o640); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeTextFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(value), 0o640); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func int64Ptr(value int64) *int64       { return &value }
func float64Ptr(value float64) *float64 { return &value }

func buildFixtureStore(t *testing.T) (string, *testClock) {
	t.Helper()
	root := t.TempDir()
	clock := &testClock{current: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	now := clock.current

	success := factory.RunManifest{
		RunID: "run-success", ChangeID: "change-success", Scope: "default",
		Repository: "github.com/acme/api", RepositoryKind: "remote",
		RequestedRevision: "aaaa1111", BaselineSHA: "aaaa1111",
		AgentHarness: "opencode2", VerificationProfile: "default",
		SandboxID: "sandbox-a", FactoryResult: factory.StateSucceeded,
		AgentResult: factory.OutcomeSuccess, VerificationResult: factory.OutcomeSuccess,
		StartedAt: now.Add(-2 * time.Hour), CompletedAt: now.Add(-2*time.Hour + 10*time.Second),
		Duration:      10 * time.Second,
		CleanupResult: factory.CleanupResult{Attempted: true, Outcome: factory.OutcomeSuccess, Verified: true, FinishedAt: now.Add(-2 * time.Hour)},
		TokensIn:      int64Ptr(100), TokensOut: int64Ptr(50), InferenceCost: float64Ptr(0.25),
	}
	failed := factory.RunManifest{
		RunID: "run-failed", Scope: "default",
		Repository: "github.com/acme/web", RequestedRevision: "bbbb2222",
		AgentHarness: "conformance", VerificationProfile: "default",
		SandboxID: "sandbox-b", FactoryResult: factory.StateVerificationFailed,
		AgentResult: factory.OutcomeSuccess, VerificationResult: factory.OutcomeFailed,
		StartedAt: now.Add(-3 * time.Hour), CompletedAt: now.Add(-3*time.Hour + 20*time.Second),
		Duration:      20 * time.Second,
		CleanupResult: factory.CleanupResult{Attempted: true, Outcome: factory.OutcomeSuccess, Verified: true},
	}
	security := factory.RunManifest{
		RunID: "run-security", Scope: "default",
		Repository: "github.com/acme/api", RequestedRevision: "cccc3333",
		AgentHarness: "opencode2", VerificationProfile: "default",
		SandboxID: "sandbox-c", FactoryResult: factory.StateQuarantined,
		AgentResult: factory.OutcomeSuccess, VerificationResult: factory.OutcomeSkipped,
		StartedAt: now.Add(-1 * time.Hour), CompletedAt: now.Add(-time.Hour + 5*time.Second),
		Duration:      5 * time.Second,
		CleanupResult: factory.CleanupResult{Attempted: true, Outcome: factory.OutcomeSuccess, Verified: true},
		Hardening: &factory.HardeningEvidence{
			Behavior: &threatmon.Report{
				RulesetVersion: "1",
				Findings:       []threatmon.Finding{{RuleID: "PE-001", Category: "privilege-escalation", Severity: threatmon.SeverityHigh, Stream: "stdout", Line: 3}},
				Tripped:        true, MaxSeverity: threatmon.SeverityHigh,
			},
			GateIntegrity: &factory.GateIntegrity{Checked: true, Modified: []string{"build.sh"}},
			EgressProbe: &factory.EgressProbeResult{
				Policy:     "local-model",
				Canary:     factory.ProbeObservation{URL: "https://example.com/", Reachable: true, Decided: true},
				Metadata:   factory.ProbeObservation{URL: "http://169.254.169.254/", Reachable: false, Decided: false},
				Violations: []string{"canary reachable while policy expects deny"},
			},
			Alerts: []factory.AlertRecord{{Kind: "run.quarantined", Severity: "HIGH", Summary: "quarantined", Delivered: false, At: now.Add(-time.Hour)}},
		},
	}
	old := factory.RunManifest{
		RunID: "run-old", Scope: "default", Repository: "github.com/acme/old",
		AgentHarness: "conformance", VerificationProfile: "default",
		FactoryResult: factory.StateSucceeded, StartedAt: now.AddDate(0, 0, -60),
		CompletedAt: now.AddDate(0, 0, -60).Add(time.Second), Duration: time.Second,
		CleanupResult: factory.CleanupResult{Attempted: true, Outcome: factory.OutcomeSuccess, Verified: true},
	}

	for _, manifest := range []factory.RunManifest{success, failed, security, old} {
		dir := filepath.Join(root, "runs", manifest.RunID)
		writeJSONFile(t, filepath.Join(dir, "run.json"), manifest)
	}
	writeJSONFile(t, filepath.Join(root, "runs", "run-success", "task.json"), taskFile{
		Task: "Change the greeting to hello factory", TaskHash: "hash-success",
		AgentHarness: "opencode2", VerificationProfile: "default", Scope: "default",
		RecordedAt: now.Add(-2 * time.Hour),
	})
	writeTextFile(t, filepath.Join(root, "runs", "run-success", "verification", "result.json"), `{
  "passed": true,
  "profile": "default",
  "duration": 5000000000,
  "steps": [
    {"id":"build","description":"build","argv":["./build.sh"],"exit_code":0,"passed":true,"mandatory":true,"timed_out":false,"duration":1000000000}
  ]
}`)
	writeTextFile(t, filepath.Join(root, "runs", "run-success", "changes.patch"), "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n-old\n+new\n+line\n")
	writeTextFile(t, filepath.Join(root, "runs", "run-success", "agent", "stdout.log"), "agent output\n")
	writeTextFile(t, filepath.Join(root, "runs", "run-success", "audit", "actions.jsonl"), `{"at":"2026-10-27T11:00:00Z","action":"review-decision","run_id":"run-success","actor":"operator","outcome":"SUCCESS"}`+"\n")
	writeTextFile(t, filepath.Join(root, "runs", "run-success", "inventory.json"), "{}\n")

	writeJSONFile(t, filepath.Join(root, "changes", "change-success.json"), factory.Change{
		ChangeID: "change-success", Scope: "default", Repository: "github.com/acme/api",
		RequestedRevision: "aaaa1111", Task: "Change the greeting", TaskHash: "hash-success",
		CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-time.Hour), Status: factory.ChangeInReview,
		Activations: []factory.Activation{{RunID: "run-success", Reason: "initial", CreatedAt: now.Add(-2 * time.Hour)}},
		Attempts:    1, LastRunID: "run-success", LastResult: factory.StateSucceeded,
	})
	writeJSONFile(t, filepath.Join(root, "changes", "change-done.json"), factory.Change{
		ChangeID: "change-done", Scope: "default", Repository: "github.com/acme/web",
		RequestedRevision: "bbbb2222", CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour),
		Status: factory.ChangeDone, Attempts: 1, LastRunID: "run-failed", LastResult: factory.StateSucceeded,
		Activations: []factory.Activation{{RunID: "run-failed", Reason: "initial", CreatedAt: now.Add(-3 * time.Hour)}},
	})
	return root, clock
}

func newTestServer(t *testing.T, root string, clock *testClock, mutate func(*Config)) *httptest.Server {
	t.Helper()
	cfg := Config{
		DataDir:     root,
		ReadToken:   testToken,
		Environment: "test",
		Live:        false,
		Version:     "0.7.0-test",
		Now:         clock.Now,
		Static: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><body>console</body></html>")},
		},
	}
	// Start from the real default configuration so verification profiles and
	// hardening defaults behave exactly as in production, then remove every
	// network target the test must not dial.
	cfg.Factory = factory.Default()
	cfg.Factory.Storage.DataDir = root
	cfg.Factory.Cube.APIKey = ""
	cfg.Factory.Cube.APIURL = ""
	cfg.Factory.Temporal.HostPort = ""
	cfg.Factory.Hardening.AcknowledgeOpenEgress = true
	cfg.Factory.Models = map[string]factory.ModelConfig{
		"test-model": {Provider: "openai", Model: "gpt-test", APIKeyEnv: "SECRET_MODEL_KEY", BaseURL: "https://models.invalid/v1"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	server, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(server.Close)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return httpServer
}

func request(t *testing.T, server *httptest.Server, method, path, token string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response.StatusCode, body, response.Header
}

func decode(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode %s: %v", string(body)[:minInt(len(body), 400)], err)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestAuthentication(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)

	status, _, header := request(t, server, http.MethodGet, "/api/v1/console/overview", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", status)
	}
	if got := header.Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	if status, _, _ := request(t, server, http.MethodGet, "/api/v1/console/overview", "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", status)
	}
	status, body, header := request(t, server, http.MethodGet, "/api/v1/console/session", testToken)
	if status != http.StatusOK {
		t.Fatalf("session status = %d: %s", status, body)
	}
	if got := header.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Fatalf("missing CSP: %q", got)
	}
	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var session SessionResponse
	decode(t, body, &session)
	if !session.Authenticated || session.FactoryVersion != factory.Version || session.Capabilities.Actions {
		t.Fatalf("unexpected session: %+v", session)
	}
}

func TestHealthzIsPublic(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/healthz", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("healthz status=%d body=%s", status, body)
	}
}

func TestOverviewAggregatesEvidence(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/overview?window=30d", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var overview Overview
	decode(t, body, &overview)
	if overview.KPIs.Runs.Value != 3 {
		t.Fatalf("runs = %v, want 3 (the 60-day-old run must be excluded)", overview.KPIs.Runs.Value)
	}
	if overview.KPIs.SecurityStops.Value != 1 {
		t.Fatalf("security stops = %v, want 1", overview.KPIs.SecurityStops.Value)
	}
	if overview.KPIs.Reviews.Value != 1 {
		t.Fatalf("pending reviews = %v, want 1", overview.KPIs.Reviews.Value)
	}
	if !containsAttentionKind(overview.Attention, "security_stop") {
		t.Fatalf("attention is missing the security stop: %+v", overview.Attention)
	}
	if overview.Security.Tripped != 1 || overview.Security.EgressViolations != 1 {
		t.Fatalf("unexpected security summary: %+v", overview.Security)
	}
	if overview.Health.Status != "attention" {
		t.Fatalf("health status = %q, want attention", overview.Health.Status)
	}
	if overview.KPIs.CostUSD.Sample != 1 || overview.KPIs.CostUSD.Value != 0.25 {
		t.Fatalf("cost KPI = %+v", overview.KPIs.CostUSD)
	}
	if overview.KPIs.InFlight.Available {
		t.Fatalf("in-flight should be unavailable when live orchestration is off")
	}
	if len(overview.Series.Points) == 0 {
		t.Fatalf("expected series points")
	}
}

func containsAttentionKind(items []AttentionItem, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func TestRunsFilteringAndPagination(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)

	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/runs?window=all&limit=2", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var page RunsResponse
	decode(t, body, &page)
	if page.Page.Total != 4 || len(page.Runs) != 2 || page.Page.NextCursor == "" {
		t.Fatalf("unexpected page: %+v", page.Page)
	}
	if page.Runs[0].RunID != "run-security" {
		t.Fatalf("default sort should be newest first, got %s", page.Runs[0].RunID)
	}

	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/runs?window=all&state=SUCCEEDED", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	decode(t, body, &page)
	if page.Page.Total != 2 {
		t.Fatalf("SUCCEEDED total = %d, want 2", page.Page.Total)
	}
	for _, run := range page.Runs {
		if run.FactoryResult != "SUCCEEDED" {
			t.Fatalf("state filter leaked %q", run.FactoryResult)
		}
	}

	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/runs?window=all&query=acme%2Fweb", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	decode(t, body, &page)
	if page.Page.Total != 1 || page.Runs[0].RunID != "run-failed" {
		t.Fatalf("query filter = %+v", page.Runs)
	}

	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/runs?window=all&sort=duration", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	decode(t, body, &page)
	if page.Runs[0].RunID != "run-failed" {
		// Bare "duration" sorts descending: failed 20s, success 10s,
		// security 5s, old 1s.
		t.Fatalf("descending duration sort first = %s, want run-failed", page.Runs[0].RunID)
	}
	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/runs?window=all&sort=%2Bduration", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	decode(t, body, &page)
	if page.Runs[0].RunID != "run-old" {
		t.Fatalf("ascending duration sort first = %s, want run-old", page.Runs[0].RunID)
	}
}

func TestRunDetailContainsEvidence(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/runs/run-success", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var detail RunDetail
	decode(t, body, &detail)
	if detail.Manifest == nil || detail.Manifest.RunID != "run-success" {
		t.Fatalf("manifest missing: %+v", detail)
	}
	if detail.Task == nil || !strings.Contains(detail.Task.Task, "hello factory") {
		t.Fatalf("task missing: %+v", detail.Task)
	}
	if !detail.Verification.Available || len(detail.Verification.Steps) != 1 {
		t.Fatalf("verification = %+v", detail.Verification)
	}
	if !detail.Patch.Available || detail.Patch.Additions != 2 || detail.Patch.Deletions != 1 {
		t.Fatalf("patch = %+v", detail.Patch)
	}
	if len(detail.Artifacts) == 0 {
		t.Fatalf("expected artifacts")
	}
	if len(detail.Audit) != 1 || detail.Audit[0].Action != factory.AuditReviewDecision {
		t.Fatalf("audit = %+v", detail.Audit)
	}
	if detail.Change == nil || detail.Change.ChangeID != "change-success" {
		t.Fatalf("change = %+v", detail.Change)
	}
	if !strings.Contains(detail.Actions.ApproveCommand, "factory review run-success --approve") {
		t.Fatalf("actions = %+v", detail.Actions)
	}
}

func TestPatchAndArtifactEndpoints(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)

	status, body, header := request(t, server, http.MethodGet, "/api/v1/console/runs/run-success/patch", testToken)
	if status != http.StatusOK {
		t.Fatalf("patch status = %d: %s", status, body)
	}
	if !strings.HasPrefix(header.Get("Content-Type"), "text/plain") || !strings.Contains(string(body), "diff --git") {
		t.Fatalf("unexpected patch response: %s", body)
	}

	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/runs/run-success/artifacts?path=task.json", testToken)
	if status != http.StatusOK {
		t.Fatalf("artifact status = %d: %s", status, body)
	}
	var artifact struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	decode(t, body, &artifact)
	if artifact.Path != "task.json" || !strings.Contains(artifact.Content, "hello factory") {
		t.Fatalf("artifact = %+v", artifact)
	}

	for _, path := range []string{"..%2F..%2Fetc%2Fpasswd", "hardening%2F..%2F..%2Frun.json", "run.json%00.txt"} {
		status, _, _ = request(t, server, http.MethodGet, "/api/v1/console/runs/run-success/artifacts?path="+path, testToken)
		if status == http.StatusOK {
			t.Fatalf("path %q should not be previewable", path)
		}
	}
}

func TestMissingAndInvalidRuns(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	for _, path := range []string{"/api/v1/console/runs/does-not-exist", "/api/v1/console/runs/..%2F..%2Fetc"} {
		status, _, _ := request(t, server, http.MethodGet, path, testToken)
		if status != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, status)
		}
	}
}

func TestChangesAndReviews(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)

	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/changes?window=all&status=IN_REVIEW", testToken)
	if status != http.StatusOK {
		t.Fatalf("changes status = %d: %s", status, body)
	}
	var changes ChangesResponse
	decode(t, body, &changes)
	if changes.Page.Total != 1 || changes.Changes[0].ChangeID != "change-success" {
		t.Fatalf("changes = %+v", changes)
	}

	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/changes/change-success", testToken)
	if status != http.StatusOK {
		t.Fatalf("detail status = %d: %s", status, body)
	}
	var detail ChangeDetail
	decode(t, body, &detail)
	if len(detail.Runs) != 1 || detail.Runs[0].RunID != "run-success" {
		t.Fatalf("change runs = %+v", detail.Runs)
	}
	if !detail.Review.Pending || detail.Review.Actionable {
		t.Fatalf("durable review record should be pending but not actionable without a live gate: %+v", detail.Review)
	}
	if detail.Review.ApproveCommand != "" || detail.Review.RejectCommand != "" || detail.Review.Note == "" {
		t.Fatalf("durable review record must withhold commands and explain why: %+v", detail.Review)
	}

	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/reviews", testToken)
	if status != http.StatusOK {
		t.Fatalf("reviews status = %d: %s", status, body)
	}
	var reviews ReviewsResponse
	decode(t, body, &reviews)
	if reviews.Total != 1 || len(reviews.Items) != 1 {
		t.Fatalf("reviews = %+v", reviews)
	}
	if reviews.Items[0].Kind != "in_review" || reviews.Items[0].Change == nil {
		t.Fatalf("review item = %+v", reviews.Items[0])
	}
	if reviews.Items[0].Actionable || reviews.Items[0].Note == "" || reviews.Items[0].ApproveCommand != "" {
		t.Fatalf("durable review item must not advertise a no-op command: %+v", reviews.Items[0])
	}
}

func TestSecurityAggregate(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/security?window=30d", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var security SecurityResponse
	decode(t, body, &security)
	if security.Inspected != 3 || security.Tripped != 1 || security.GateTampered != 1 {
		t.Fatalf("security counts = %+v", security)
	}
	if security.EgressViolations != 1 || security.Egress.CanaryReachable != 1 {
		t.Fatalf("egress = %+v", security.Egress)
	}
	if security.AlertsUndelivered != 1 || len(security.Alerts) != 1 {
		t.Fatalf("alerts = %+v", security.Alerts)
	}
	if len(security.Rules) != 1 || security.Rules[0].RuleID != "PE-001" {
		t.Fatalf("rules = %+v", security.Rules)
	}
	if !security.Posture.EgressProbeEnabled || !security.Posture.BehaviorMonitorEnabled {
		t.Fatalf("posture should default both protections on: %+v", security.Posture)
	}
}

func TestOperationsAndCatalogRedaction(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, func(cfg *Config) {
		cfg.Factory.Cube.APIKey = "cube-super-secret-value"
		cfg.Factory.Temporal.HostPort = "127.0.0.1:1" // nothing listens here; probe must not fail the page
	})
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/operations", testToken)
	if status != http.StatusOK {
		t.Fatalf("operations status = %d: %s", status, body)
	}
	var operations OperationsResponse
	decode(t, body, &operations)
	if operations.Cleanup.Verified != 4 || operations.Cleanup.Failed != 0 {
		t.Fatalf("cleanup = %+v", operations.Cleanup)
	}
	if operations.LiveSandboxes.Available {
		t.Fatalf("live sandboxes should be unavailable when live is disabled")
	}
	if operations.LiveSandboxes.AttributionAvailable {
		t.Fatalf("sandbox attribution cannot be available when live orchestration is disabled")
	}
	if len(operations.Probes) == 0 {
		t.Fatalf("expected probes for the configured Temporal target")
	}
	for _, probe := range operations.Probes {
		if probe.ID == "temporal" && probe.State != "unreachable" {
			t.Fatalf("temporal probe = %+v, want unreachable", probe)
		}
	}

	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/catalog", testToken)
	if status != http.StatusOK {
		t.Fatalf("catalog status = %d: %s", status, body)
	}
	if strings.Contains(string(body), "cube-super-secret-value") {
		t.Fatalf("catalog leaked the Cube API key")
	}
	var catalog CatalogResponse
	decode(t, body, &catalog)
	if catalog.Cube.CredentialsPresent != true {
		t.Fatalf("catalog should report credential presence, got %+v", catalog.Cube)
	}
	if len(catalog.Models) != 1 || catalog.Models[0].APIKeyEnv != "SECRET_MODEL_KEY" {
		t.Fatalf("models = %+v", catalog.Models)
	}
	if len(catalog.Harnesses) == 0 || len(catalog.Verification) == 0 {
		t.Fatalf("catalog is missing harnesses or verification profiles")
	}
}

func TestSearch(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/search?q=acme", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var search SearchResponse
	decode(t, body, &search)
	if len(search.Results) == 0 {
		t.Fatalf("expected search results")
	}
	foundRun, foundChange := false, false
	for _, result := range search.Results {
		if result.Kind == "run" && result.Href == "#/runs/run-success" {
			foundRun = true
		}
		if result.Kind == "change" && result.ID == "change-success" {
			foundChange = true
		}
	}
	if !foundRun || !foundChange {
		t.Fatalf("search results = %+v", search.Results)
	}
}

func TestStaticAppIsServed(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/", "")
	if status != http.StatusOK || !strings.Contains(string(body), "console") {
		t.Fatalf("static status = %d, body = %s", status, body)
	}
	status, _, _ = request(t, server, http.MethodGet, "/some/client/route", "")
	if status != http.StatusOK {
		t.Fatalf("SPA fallback status = %d", status)
	}
}

func TestIndexRefreshIsIncremental(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	_, body, _ := request(t, server, http.MethodGet, "/api/v1/console/runs?window=all", testToken)
	var page RunsResponse
	decode(t, body, &page)
	if page.Page.Total != 4 {
		t.Fatalf("initial total = %d", page.Page.Total)
	}

	manifest := factory.RunManifest{
		RunID: "run-late", Scope: "default", Repository: "github.com/acme/late",
		AgentHarness: "conformance", FactoryResult: factory.StateSucceeded,
		StartedAt: clock.current.Add(-30 * time.Minute), CompletedAt: clock.current.Add(-29 * time.Minute),
		Duration: time.Minute, CleanupResult: factory.CleanupResult{Attempted: true, Outcome: factory.OutcomeSuccess, Verified: true},
	}
	dir := filepath.Join(root, "runs", "run-late")
	writeJSONFile(t, filepath.Join(dir, "run.json"), manifest)
	// Force a refresh by advancing beyond the configured interval.
	clock.current = clock.current.Add(2 * time.Minute)
	_, body, _ = request(t, server, http.MethodGet, "/api/v1/console/runs?window=all", testToken)
	decode(t, body, &page)
	if page.Page.Total != 5 {
		t.Fatalf("after refresh total = %d, want 5", page.Page.Total)
	}
	if page.Runs[0].RunID != "run-late" {
		t.Fatalf("newest run = %s", page.Runs[0].RunID)
	}
}

func TestWindowsAndParsing(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw      string
		expected string
		started  bool
	}{
		{"", "30d", true}, {"24h", "24h", true}, {"7d", "7d", true}, {"90d", "90d", true}, {"all", "all", false}, {"nonsense", "30d", true},
	} {
		spec := parseWindow(tc.raw, now)
		if spec.Key != tc.expected || spec.HasStart != tc.started {
			t.Fatalf("parseWindow(%q) = %+v", tc.raw, spec)
		}
	}
	stats := parsePatchStats("diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1,2 @@\n-old\n+new\n+other\n")
	if stats.Additions != 2 || stats.Deletions != 1 || len(stats.Files) != 1 || stats.Files[0].Path != "main.go" {
		t.Fatalf("patch stats = %+v", stats)
	}
}

func TestLoopbackListenerRules(t *testing.T) {
	for address, expected := range map[string]bool{
		"127.0.0.1:7331": true, "localhost:7331": true, "[::1]:7331": true,
		"0.0.0.0:7331": false, ":7331": false, "10.0.0.5:7331": false,
	} {
		if got := isLoopbackListen(address); got != expected {
			t.Fatalf("isLoopbackListen(%q) = %v, want %v", address, got, expected)
		}
	}
	if _, err := New(Config{DataDir: t.TempDir(), Live: false}); err == nil {
		t.Fatalf("expected New to require a token")
	}
	if _, err := New(Config{DataDir: t.TempDir(), ReadToken: "short"}); err == nil {
		t.Fatalf("expected New to reject a short token")
	}
}

func TestSummarizeHardeningDistinguishesUnmeasuredControls(t *testing.T) {
	empty := summarizeHardening(&factory.HardeningEvidence{})
	if empty.Measured || empty.BehaviorMeasured || empty.EgressMeasured {
		t.Fatalf("missing hardening evidence must not be measured: %+v", empty)
	}
	egressOnly := summarizeHardening(&factory.HardeningEvidence{EgressProbe: &factory.EgressProbeResult{}})
	if !egressOnly.Measured || !egressOnly.EgressMeasured || egressOnly.BehaviorMeasured {
		t.Fatalf("egress-only evidence should not imply a behavior scan: %+v", egressOnly)
	}
	behavior := summarizeHardening(&factory.HardeningEvidence{Behavior: &threatmon.Report{}})
	if !behavior.Measured || !behavior.BehaviorMeasured {
		t.Fatalf("recorded behavior scan should be measured: %+v", behavior)
	}
}

func TestSecurityEmptyWindowEmitsArrays(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	// Move the clock past every fixture and ask for a 24h window: the response
	// must carry empty arrays, never JSON null, for list-shaped fields.
	clock.current = clock.current.AddDate(0, 0, 10)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/security?window=24h", testToken)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	for _, field := range []string{`"entries":[]`, `"alerts":[]`, `"stops":[]`, `"rules":[]`} {
		if !strings.Contains(string(body), field) {
			t.Fatalf("security response is missing %s: %s", field, body)
		}
	}
	if strings.Contains(string(body), `"entries":null`) {
		t.Fatalf("security response contains a null list: %s", body)
	}
}

func TestStaleIndexIsReportedNotHidden(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/overview", testToken)
	if status != http.StatusOK {
		t.Fatalf("initial overview status = %d: %s", status, body)
	}
	var overview Overview
	decode(t, body, &overview)
	if overview.Index.Stale || overview.Index.Runs != 4 {
		t.Fatalf("initial index = %+v", overview.Index)
	}

	// Make the evidence directory unreadable to the scan while keeping the
	// previously indexed data alive.
	runsDir := filepath.Join(root, "runs")
	moved := filepath.Join(root, "runs-moved")
	if err := os.Rename(runsDir, moved); err != nil {
		t.Fatalf("rename runs: %v", err)
	}
	defer func() {
		_ = os.Remove(filepath.Join(root, "runs"))
		_ = os.Rename(moved, runsDir)
	}()
	if err := os.WriteFile(filepath.Join(root, "runs"), []byte("not a directory"), 0o640); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	clock.current = clock.current.Add(2 * time.Minute)
	status, body, _ = request(t, server, http.MethodGet, "/api/v1/console/overview", testToken)
	if status != http.StatusOK {
		t.Fatalf("stale overview status = %d: %s", status, body)
	}
	decode(t, body, &overview)
	if !overview.Index.Stale || overview.Index.ScanError == "" {
		t.Fatalf("expected a stale index with an error, got %+v", overview.Index)
	}
	if overview.Index.Runs != 4 {
		t.Fatalf("stale index should retain the last complete projection, got %d runs", overview.Index.Runs)
	}
}

func TestArtifactSymlinkCannotEscapeRunDirectory(t *testing.T) {
	root, clock := buildFixtureStore(t)
	outside := filepath.Join(root, "outside-secret.txt")
	writeTextFile(t, outside, "top secret host content")
	link := filepath.Join(root, "runs", "run-success", "verification", "escape.txt")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/runs/run-success/artifacts?path=verification%2Fescape.txt", testToken)
	if status == http.StatusOK && strings.Contains(string(body), "top secret") {
		t.Fatalf("artifact endpoint followed an escaping symlink: %s", body)
	}
}

func TestPatchPathComesFromTheFactoryNotTheManifest(t *testing.T) {
	root, clock := buildFixtureStore(t)
	secret := filepath.Join(root, "outside.patch")
	writeTextFile(t, secret, "SECRET HOST PATCH CONTENT")
	// Corrupt the manifest's patch pointer; the handler must ignore it.
	manifestPath := filepath.Join(root, "runs", "run-success", "run.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest factory.RunManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	manifest.Patch = "../outside.patch"
	writeJSONFile(t, manifestPath, manifest)
	server := newTestServer(t, root, clock, nil)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/runs/run-success/patch", testToken)
	if status != http.StatusOK {
		t.Fatalf("patch status = %d: %s", status, body)
	}
	if strings.Contains(string(body), "SECRET HOST PATCH CONTENT") {
		t.Fatalf("patch endpoint followed the manifest pointer outside the run directory")
	}
	if !strings.Contains(string(body), "diff --git") {
		t.Fatalf("expected the factory-owned patch, got %s", body)
	}
}

func TestServeRefusesNoAuthOnNonLoopback(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server, err := New(Config{
		DataDir: root, AllowNoAuth: true, Live: false, Now: clock.Now,
		Static: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer server.Close()
	if err := server.Serve(context.Background(), "0.0.0.0:0", nil); err == nil {
		t.Fatalf("expected Serve to refuse no-auth on a non-loopback listener")
	}
}

func TestQueryAndFacetBounds(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, nil)
	long := strings.Repeat("a", 5000)
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/runs?window=all&query="+long, testToken)
	if status != http.StatusOK {
		t.Fatalf("long query status = %d: %s", status, body)
	}
	// The query is bounded, so it cannot match a 5,000-character needle.
	if !strings.Contains(string(body), `"total":0`) {
		t.Fatalf("bounded query unexpectedly matched: %s", body[:minInt(len(body), 300)])
	}
	states := strings.Repeat("state=SUCCEEDED&", maxFacetValues+10) + "state=SUCCEEDED"
	status, _, _ = request(t, server, http.MethodGet, "/api/v1/console/runs?window=all&"+states, testToken)
	if status != http.StatusOK {
		t.Fatalf("facet flood status = %d", status)
	}
}

func TestShellArgQuotesUnsafeRunIDs(t *testing.T) {
	if got := shellArg("run-safe_1.2"); got != "run-safe_1.2" {
		t.Fatalf("safe id should stay readable, got %q", got)
	}
	unsafe := []string{"id with space", "id;rm -rf /", "id$(id)", "id'quote"}
	for _, value := range unsafe {
		got := shellArg(value)
		if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
			t.Fatalf("shellArg(%q) = %q, want a single-quoted argument", value, got)
		}
	}
	actions := runActions("demo$(id)")
	if !strings.Contains(actions.CancelCommand, "'demo$(id)'") {
		t.Fatalf("cancel command did not quote the run id: %q", actions.CancelCommand)
	}
}

func TestArtifactTailStartsAtUTF8Boundary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.log")
	// Repeated 3-byte runes guarantee that a byte-offset tail starts mid-rune.
	content := strings.Repeat("€", maxArtifactBytes/3+100)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	data, truncated, err := readArtifactContent(file, info.Size())
	if err != nil {
		t.Fatalf("readArtifactContent: %v", err)
	}
	if !truncated {
		t.Fatalf("expected a truncated tail")
	}
	if !utf8.Valid(data) {
		t.Fatalf("tail starts inside a rune and is not valid UTF-8")
	}
}

func TestDetailHrefEncodesIDAsOneSegment(t *testing.T) {
	cases := map[string]string{
		"run-123":    "#/runs/run-123",
		"demo%20run": "#/runs/demo%2520run",
		"a b":        "#/runs/a%20b",
		"a#b":        "#/runs/a%23b",
		"a/b":        "#/runs/a%2Fb",
	}
	for id, want := range cases {
		if got := detailHref("#/runs/", id); got != want {
			t.Fatalf("detailHref(%q) = %q, want %q", id, got, want)
		}
	}
	if got := detailHref("#/changes/", "demo%20run"); got != "#/changes/demo%2520run" {
		t.Fatalf("change detail href = %q", got)
	}
}

func TestSummarizeRunOmitsZeroTimestamps(t *testing.T) {
	summary := summarizeRun("run-notime", factory.RunManifest{RunID: "run-notime", FactoryResult: factory.StateInvalidRequest})
	if summary.StartedAt != nil || summary.CompletedAt != nil {
		t.Fatalf("zero timestamps must be omitted, got %+v/%+v", summary.StartedAt, summary.CompletedAt)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "0001-01-01") {
		t.Fatalf("summary rendered a year-1 timestamp: %s", encoded)
	}
}
