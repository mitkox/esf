package factory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// RunMemoKey is the memo field carrying the operator-visible run summary.
//
// A memo is used rather than a custom search attribute because search
// attributes must be registered on the cluster, while a memo works on every
// deployment without changing it. The trade-off is that filtering happens
// client-side after the list call, which is acceptable for the volumes a single
// factory produces.
const RunMemoKey = "factory_run"

// RunMemo is the summary attached to a run's workflow at start.
type RunMemo struct {
	Harness    string `json:"harness,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// RunMemoFor builds the memo payload for a request.
func RunMemoFor(req RunRequest) map[string]any {
	return map[string]any{RunMemoKey: RunMemo{
		Harness:    req.AgentHarness,
		Scope:      req.Scope,
		Repository: req.Repository,
	}}
}

// CancelRun cancels a run's workflow.
//
// It is the operator's mid-flight control. Without it, stopping a run means
// reaching for the Temporal CLI during an incident, which is exactly when a
// missing product control costs the most.
func CancelRun(ctx context.Context, c client.Client, runID string) error {
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("a run ID is required")
	}
	if err := c.CancelWorkflow(ctx, WorkflowIDForRun(runID), ""); err != nil {
		return fmt.Errorf("cancel run %s: %w", runID, err)
	}
	return nil
}

// HaltFilter selects running runs to stop. Empty fields match everything.
type HaltFilter struct {
	Harness    string
	Scope      string
	Repository string
}

// Matches reports whether a memo satisfies the filter.
func (f HaltFilter) Matches(m RunMemo) bool {
	if v := strings.TrimSpace(f.Harness); v != "" && !strings.EqualFold(strings.TrimSpace(m.Harness), v) {
		return false
	}
	if v := strings.TrimSpace(f.Scope); v != "" && !strings.EqualFold(strings.TrimSpace(m.Scope), v) {
		return false
	}
	if v := strings.TrimSpace(f.Repository); v != "" && !strings.Contains(m.Repository, v) {
		return false
	}
	return true
}

// OpenRun is one running workflow, with the summary an operator filters on.
type OpenRun struct {
	RunID      string    `json:"run_id"`
	WorkflowID string    `json:"workflow_id"`
	Harness    string    `json:"harness,omitempty"`
	Scope      string    `json:"scope,omitempty"`
	Repository string    `json:"repository,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

// ListOpenRuns returns the running factory workflows matching a filter.
//
// It pages through every result. A single unpaginated call would silently miss
// runs past the first page, which is exactly the case that matters during a
// halt: "some runs are still executing and the command said it stopped them".
func ListOpenRuns(ctx context.Context, c client.Client, f HaltFilter) ([]OpenRun, error) {
	query := fmt.Sprintf("ExecutionStatus = \"Running\" AND WorkflowType = \"%s\"", WorkflowName)
	var runs []OpenRun
	var pageToken []byte
	for {
		resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         query,
			NextPageToken: pageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list running runs: %w", err)
		}
		for _, info := range resp.GetExecutions() {
			workflowID := info.GetExecution().GetWorkflowId()
			runID, ok := RunIDFromWorkflowID(workflowID)
			if !ok || runID == "" {
				continue
			}
			memo := decodeRunMemo(info.GetMemo())
			if !f.Matches(memo) {
				continue
			}
			runs = append(runs, OpenRun{
				RunID:      runID,
				WorkflowID: workflowID,
				Harness:    memo.Harness,
				Scope:      memo.Scope,
				Repository: memo.Repository,
				StartedAt:  info.GetStartTime().AsTime(),
			})
		}
		pageToken = resp.GetNextPageToken()
		if len(pageToken) == 0 {
			break
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].RunID < runs[j].RunID
		}
		return runs[i].StartedAt.Before(runs[j].StartedAt)
	})
	return runs, nil
}

// decodeRunMemo reads the factory summary from a workflow memo, tolerating a
// workflow started before the memo existed.
func decodeRunMemo(memo *commonpb.Memo) RunMemo {
	if memo == nil {
		return RunMemo{}
	}
	payload, ok := memo.GetFields()[RunMemoKey]
	if !ok || payload == nil {
		return RunMemo{}
	}
	var out RunMemo
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &out); err != nil {
		return RunMemo{}
	}
	return out
}

// HaltRuns cancels every running workflow matching the filter.
//
// It returns the runs it attempted and an error that joins every cancellation
// failure, so a partial halt is never reported as a complete one.
func HaltRuns(ctx context.Context, c client.Client, f HaltFilter) ([]OpenRun, error) {
	runs, err := ListOpenRuns(ctx, c, f)
	if err != nil {
		return nil, err
	}
	var failures []string
	for _, run := range runs {
		if err := c.CancelWorkflow(ctx, run.WorkflowID, ""); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", run.RunID, err))
		}
	}
	if len(failures) > 0 {
		return runs, fmt.Errorf("some runs could not be cancelled: %s", strings.Join(failures, "; "))
	}
	return runs, nil
}
