package factory

import (
	"context"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/threatmon"
)

func TestHaltFilterMatches(t *testing.T) {
	memo := RunMemo{Harness: "opencode2", Scope: "payments", Repository: "https://github.com/acme/payments"}
	cases := []struct {
		name   string
		filter HaltFilter
		want   bool
	}{
		{"empty matches everything", HaltFilter{}, true},
		{"harness case-insensitive", HaltFilter{Harness: "OpenCode2"}, true},
		{"harness mismatch", HaltFilter{Harness: "unreal"}, false},
		{"scope mismatch", HaltFilter{Scope: "other"}, false},
		{"repository substring", HaltFilter{Repository: "payments"}, true},
		{"repository miss", HaltFilter{Repository: "ledger"}, false},
		{"all fields", HaltFilter{Harness: "opencode2", Scope: "payments", Repository: "acme"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Matches(memo); got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunMemoForCarriesFilterableFields(t *testing.T) {
	memo := RunMemoFor(RunRequest{AgentHarness: "opencode2", Scope: "payments", Repository: "https://github.com/acme/payments"})
	raw, ok := memo[RunMemoKey]
	if !ok {
		t.Fatalf("memo is missing %q: %v", RunMemoKey, memo)
	}
	decoded, ok := raw.(RunMemo)
	if !ok {
		t.Fatalf("memo value is %T, want RunMemo", raw)
	}
	if decoded.Harness != "opencode2" || decoded.Scope != "payments" || decoded.Repository == "" {
		t.Fatalf("memo = %+v", decoded)
	}
}

func TestBuildThreatReportAggregatesAcrossRuns(t *testing.T) {
	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(runID string, manifest RunManifest) {
		runStore, err := store.ForRun(runID)
		if err != nil {
			t.Fatal(err)
		}
		manifest.RunID = runID
		data := mustJSON(t, manifest)
		if err := runStore.Write(ArtifactRun, data); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()

	write("run-clean", RunManifest{
		FactoryResult: StateSucceeded,
		CompletedAt:   now,
		Hardening: &HardeningEvidence{
			Behavior: &threatmon.Report{RulesetVersion: "1"},
			EgressProbe: &EgressProbeResult{
				Policy:   "strict",
				Canary:   ProbeObservation{Reachable: false, Decided: true},
				Metadata: ProbeObservation{Reachable: false, Decided: true},
				AgentUID: 1000,
			},
		},
	})
	write("run-tripped", RunManifest{
		FactoryResult: StateQuarantined,
		CompletedAt:   now.Add(time.Minute),
		Hardening: &HardeningEvidence{
			Behavior: &threatmon.Report{
				RulesetVersion: "1",
				Tripped:        true,
				MaxSeverity:    threatmon.SeverityCritical,
				Findings: []threatmon.Finding{
					{RuleID: "CA-001", Category: "credential-access", Severity: threatmon.SeverityHigh, Stream: "stdout", Line: 1},
					{RuleID: "RS-001", Category: "reverse-shell", Severity: threatmon.SeverityCritical, Stream: "stdout", Line: 2},
				},
			},
			EgressProbe: &EgressProbeResult{
				Policy:     "strict",
				ExpectDeny: true,
				Canary:     ProbeObservation{Reachable: true, Decided: true},
				Metadata:   ProbeObservation{Reachable: false, Decided: true},
				AgentUID:   0,
				Violations: []string{"egress policy claims to deny public internet"},
			},
			GateIntegrity: &GateIntegrity{Checked: true, Modified: []string{"build.sh"}},
			Alerts:        []AlertRecord{{Kind: AlertQuarantine, Delivered: false}},
		},
	})
	write("run-blocked", RunManifest{
		FactoryResult: StateBlocked,
		CompletedAt:   now.Add(2 * time.Minute),
		Hardening:     &HardeningEvidence{Behavior: &threatmon.Report{RulesetVersion: "1"}},
	})

	report, err := BuildThreatReport(store, 10)
	if err != nil {
		t.Fatalf("BuildThreatReport: %v", err)
	}
	if report.Runs != 3 {
		t.Fatalf("runs = %d, want 3", report.Runs)
	}
	if report.RunsWithFindings != 1 || report.Tripped != 1 {
		t.Fatalf("findings=%d tripped=%d, want 1/1", report.RunsWithFindings, report.Tripped)
	}
	if report.Blocked != 1 {
		t.Fatalf("blocked = %d, want 1", report.Blocked)
	}
	if report.GateTampered != 1 {
		t.Fatalf("gate_tampered = %d, want 1", report.GateTampered)
	}
	if report.EgressViolations != 1 {
		t.Fatalf("egress_violations = %d, want 1", report.EgressViolations)
	}
	if report.AlertsUndelivered != 1 {
		t.Fatalf("alerts_undelivered = %d, want 1", report.AlertsUndelivered)
	}
	if len(report.Rules) != 2 {
		t.Fatalf("rules = %+v, want two tallies", report.Rules)
	}
	// The aggregate must cover every run even when the entry list is capped.
	capped, err := BuildThreatReport(store, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 with limit=1", len(capped.Entries))
	}
	if capped.Tripped != 1 || capped.Runs != 3 {
		t.Fatalf("capping the entry list changed the aggregates: %+v", capped)
	}
	// Entries are newest first.
	if report.Entries[0].RunID != "run-blocked" {
		t.Fatalf("entries[0] = %s, want the newest run", report.Entries[0].RunID)
	}
}

func TestBuildThreatReportIsEmptyWithoutRuns(t *testing.T) {
	store, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildThreatReport(store, 0)
	if err != nil {
		t.Fatalf("BuildThreatReport: %v", err)
	}
	if report.Runs != 0 || len(report.Rules) != 0 || len(report.Entries) != 0 {
		t.Fatalf("report = %+v, want empty", report)
	}
}

// listClient is a Temporal client stub that only implements ListWorkflow. It
// exists to prove pagination: the embedded interface is never called, so a nil
// value is safe.
type listClient struct {
	client.Client
	pages     [][]*workflowpb.WorkflowExecutionInfo
	pageToken []string
	calls     int
}

func (c *listClient) ListWorkflow(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	index := 0
	if len(req.GetNextPageToken()) > 0 {
		index = 1
	}
	if index >= len(c.pages) {
		return &workflowservice.ListWorkflowExecutionsResponse{}, nil
	}
	c.calls++
	resp := &workflowservice.ListWorkflowExecutionsResponse{Executions: c.pages[index]}
	if index+1 < len(c.pages) {
		resp.NextPageToken = []byte("next")
	}
	return resp, nil
}

func openRunInfo(t *testing.T, runID string, memo RunMemo) *workflowpb.WorkflowExecutionInfo {
	t.Helper()
	payload, err := converter.GetDefaultDataConverter().ToPayload(memo)
	if err != nil {
		t.Fatal(err)
	}
	return &workflowpb.WorkflowExecutionInfo{
		Execution: &commonpb.WorkflowExecution{WorkflowId: WorkflowIDForRun(runID)},
		Memo:      &commonpb.Memo{Fields: map[string]*commonpb.Payload{RunMemoKey: payload}},
		StartTime: timestamppb.New(time.Unix(0, 0).UTC()),
	}
}

// TestListOpenRunsPagesThroughEveryResult proves a halt cannot silently miss
// runs past the first page. "Some runs are still executing and the command said
// it stopped them" is the failure this prevents.
func TestListOpenRunsPagesThroughEveryResult(t *testing.T) {
	first := openRunInfo(t, "run-1", RunMemo{Harness: "opencode2"})
	second := openRunInfo(t, "run-2", RunMemo{Harness: "unreal"})
	third := openRunInfo(t, "run-3", RunMemo{Harness: "opencode2"})
	c := &listClient{pages: [][]*workflowpb.WorkflowExecutionInfo{{first, second}, {third}}}

	runs, err := ListOpenRuns(context.Background(), c, HaltFilter{})
	if err != nil {
		t.Fatalf("ListOpenRuns: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("runs = %d, want 3 (pagination missed a page)", len(runs))
	}
	if c.calls != 2 {
		t.Fatalf("list calls = %d, want 2", c.calls)
	}
	ids := map[string]bool{}
	for _, run := range runs {
		ids[run.RunID] = true
	}
	for _, want := range []string{"run-1", "run-2", "run-3"} {
		if !ids[want] {
			t.Fatalf("missing %s in %v", want, ids)
		}
	}

	// The filter applies across pages, not only the first one.
	filtered, err := ListOpenRuns(context.Background(), c, HaltFilter{Harness: "unreal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].RunID != "run-2" {
		t.Fatalf("filtered = %+v, want only run-2", filtered)
	}
	// Visibility memos use the same codec as workflow inputs in production.
	dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), testCodec(t, "new"))
	payload, err := dc.ToPayload(RunMemo{Harness: "unreal"})
	if err != nil {
		t.Fatal(err)
	}
	second.Memo.Fields[RunMemoKey] = payload
	filtered, err = ListOpenRuns(context.Background(), c, HaltFilter{Harness: "unreal"}, dc)
	if err != nil || len(filtered) != 1 || filtered[0].RunID != "run-2" {
		t.Fatalf("encrypted filtered memo = %+v, %v", filtered, err)
	}
	if _, err := ListOpenRuns(context.Background(), c, HaltFilter{Harness: "unreal"}); err == nil {
		t.Fatal("undecodable memo silently excluded a running workflow")
	}
}

func TestListOpenRunsSkipsUnownedWorkflowIDs(t *testing.T) {
	foreign := openRunInfo(t, "foreign", RunMemo{Harness: "opencode2"})
	foreign.Execution.WorkflowId = "other-service-foreign"
	owned := openRunInfo(t, "owned", RunMemo{Harness: "opencode2"})
	c := &listClient{pages: [][]*workflowpb.WorkflowExecutionInfo{{foreign, owned}}}
	runs, err := ListOpenRuns(context.Background(), c, HaltFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != "owned" {
		t.Fatalf("halt list included a workflow outside the factory ID namespace: %+v", runs)
	}
}
