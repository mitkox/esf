package factory

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mitkox/esf/internal/assurance"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const QualityWakeSignal = "quality-state-changed"
const CAPAWorkflowName = "CAPAWorkflow"

func controlledWorkflow(ctx workflow.Context, req RunRequest, admitted assurance.Run) (RunManifest, error) {
	acts := &Activities{}
	in := QualityRunInput{req.RunID, workflow.GetInfo(ctx).WorkflowExecution.RunID}
	var defs QualityDependencies
	if err := json.Unmarshal(admitted.Snapshot.Dependencies, &defs); err != nil {
		return RunManifest{}, err
	}
	budget := defs.Limits.TotalTimeout.Std()
	if defs.Request.TotalTimeout > 0 && (budget <= 0 || defs.Request.TotalTimeout < budget) {
		budget = defs.Request.TotalTimeout
	}
	if budget <= 0 {
		budget = 90 * time.Minute
	}
	status := RunStatus{RunID: req.RunID, ChangeID: req.ChangeID, Scope: req.Scope, State: StateRunning, StartedAt: admitted.CreatedAt, Quality: summarizeQuality(admitted)}
	if err := workflow.SetQueryHandler(ctx, "status", func() (RunStatus, error) { return status, nil }); err != nil {
		return RunManifest{}, err
	}
	compute, cancel := workflow.WithCancel(ctx)
	defer cancel()
	workflow.Go(compute, func(c workflow.Context) {
		if workflow.Sleep(c, budget) == nil {
			cancel()
		}
	})
	compute = workflow.WithActivityOptions(compute, workflow.ActivityOptions{StartToCloseTimeout: budget, HeartbeatTimeout: 2 * time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3}})
	failure := ""
	outcome := StateSucceeded
	var author QualityAuthorOutput
	status.CurrentStep = "quality-author"
	err := workflow.ExecuteActivity(compute, acts.QualityAuthor, in).Get(compute, &author)
	if err != nil {
		failure = err.Error()
		outcome = StateQualityFailed
	} else if !author.Agent.Succeeded() {
		failure = "author did not complete successfully"
		outcome = StateAgentFailed
	}
	var record assurance.Run
	if err == nil {
		status.CurrentStep = "quality-candidate"
		err = workflow.ExecuteActivity(compute, acts.QualityCandidate, in).Get(compute, &record)
		if err != nil {
			failure = err.Error()
			outcome = StateQualityFailed
		} else {
			status.Quality = summarizeQuality(record)
		}
	}
	if err == nil && failure == "" {
		for _, control := range record.Plan.Controls {
			if control.Type == "provider-ack" {
				continue
			}
			status.CurrentStep = "quality-gate/" + control.ID
			for attempt := 1; attempt <= 3; attempt++ {
				var gate assurance.GateAttempt
				err = workflow.ExecuteActivity(compute, acts.QualityGate, QualityGateInput{in.RunID, in.ExecutionID, control, attempt}).Get(compute, &gate)
				if err != nil {
					failure = err.Error()
					break
				}
				if !gate.Retryable || attempt == 3 {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}
	// Cleanup and the canonical commit must survive cancellation and compute timeout.
	cancel()
	durable, _ := workflow.NewDisconnectedContext(ctx)
	durable = workflow.WithActivityOptions(durable, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Minute, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: 30 * time.Second, MaximumAttempts: 5}})
	status.CurrentStep = "quality-cleanup"
	var cleanup CleanupResult
	if cleanupErr := workflow.ExecuteActivity(durable, acts.QualityCleanup, in).Get(durable, &cleanup); cleanupErr != nil {
		failure = fmt.Sprintf("%s; cleanup: %v", failure, cleanupErr)
		outcome = StateQualityFailed
	}
	status.SandboxGone = cleanup.Verified
	if failure == "" && cleanup.Verified {
		waitCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3}})
		for _, control := range record.Plan.Controls {
			if control.Type != "provider-ack" {
				continue
			}
			status.State = StatePaused
			status.CurrentStep = "quality-provider/" + control.ID
			input := QualityGateInput{in.RunID, in.ExecutionID, control, 1}
			for {
				var request QualityProviderRequest
				if err = workflow.ExecuteActivity(waitCtx, acts.QualityProviderCheck, input).Get(waitCtx, &request); err != nil {
					failure = err.Error()
					break
				}
				if request.Ready || request.Expired {
					var gate assurance.GateAttempt
					if err = workflow.ExecuteActivity(waitCtx, acts.QualityGate, input).Get(waitCtx, &gate); err != nil {
						failure = err.Error()
					}
					break
				}
				timerCtx, stop := workflow.WithCancel(ctx)
				selector := workflow.NewSelector(ctx)
				selector.AddFuture(workflow.NewTimer(timerCtx, qualityPollDelay(workflow.Now(ctx), request.Deadline)), func(workflow.Future) {})
				selector.AddReceive(workflow.GetSignalChannel(ctx, QualityWakeSignal), func(ch workflow.ReceiveChannel, _ bool) { var hint any; ch.Receive(ctx, &hint) })
				selector.Select(ctx)
				stop()
				if ctx.Err() != nil {
					failure = ctx.Err().Error()
					break
				}
			}
			if failure != "" {
				break
			}
		}
	}
	if failure == "" && cleanup.Verified {
		status.CurrentStep = "quality-approval"
		status.State = StatePaused
		approvalCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 3}})
		for {
			if err = workflow.ExecuteActivity(approvalCtx, acts.QualityApproval, in.RunID).Get(approvalCtx, &record); err != nil {
				failure = err.Error()
				break
			}
			status.Quality = summarizeQuality(record)
			if record.Approval.Status != "pending" {
				break
			}
			// Signals contain no decision. Timers also trigger a fresh authoritative read.
			timerCtx, stopTimer := workflow.WithCancel(ctx)
			selector := workflow.NewSelector(ctx)
			selector.AddFuture(workflow.NewTimer(timerCtx, qualityPollDelay(workflow.Now(ctx), assurance.ApprovalDeadline(record))), func(workflow.Future) {})
			selector.AddReceive(workflow.GetSignalChannel(ctx, QualityWakeSignal), func(ch workflow.ReceiveChannel, _ bool) { var hint any; ch.Receive(ctx, &hint) })
			selector.Select(ctx)
			stopTimer()
			if ctx.Err() != nil {
				failure = ctx.Err().Error()
				break
			}
		}
	}
	status.CurrentStep = "quality-finalize"
	status.State = StateRunning
	durable = workflow.WithActivityOptions(durable, workflow.ActivityOptions{StartToCloseTimeout: 5 * time.Minute, RetryPolicy: &temporal.RetryPolicy{InitialInterval: time.Second, MaximumInterval: time.Minute}})
	var manifest RunManifest
	err = workflow.ExecuteActivity(durable, acts.QualityFinalize, QualityFinalizeInput{in.RunID, failure, cleanup, outcome}).Get(durable, &manifest)
	if err != nil {
		return RunManifest{}, err
	}
	status.State = manifest.FactoryResult
	status.Quality = manifest.Quality
	status.CurrentStep = "completed"
	return manifest, nil
}

// CAPA's domain transitions are committed by the authenticated service. This
// workflow durably follows that lifecycle across worker restarts.
func CAPAWorkflow(ctx workflow.Context, id string) (assurance.CAPA, error) {
	acts := &Activities{}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute, RetryPolicy: &temporal.RetryPolicy{MaximumAttempts: 5}})
	var record assurance.CAPA
	if err := workflow.SetQueryHandler(ctx, "status", func() (assurance.CAPA, error) { return record, nil }); err != nil {
		return record, err
	}
	for i := 0; i < 500; i++ {
		if err := workflow.ExecuteActivity(ctx, acts.QualityCAPAState, id).Get(ctx, &record); err != nil {
			return record, err
		}
		if record.Status == "closed" {
			return record, nil
		}
		if err := workflow.Sleep(ctx, time.Minute); err != nil {
			return record, err
		}
	}
	return record, workflow.NewContinueAsNewError(ctx, CAPAWorkflowName, id)
}

func qualityPollDelay(now, deadline time.Time) time.Duration {
	d := deadline.Sub(now)
	if d > time.Hour {
		return time.Hour
	}
	if d <= 0 {
		return time.Second
	}
	return d
}
