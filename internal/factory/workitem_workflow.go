package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mitkox/esf/internal/forge"
)

const (
	// WorkItemWorkflowName is the registered top-level work-item workflow.
	WorkItemWorkflowName = "WorkItemWorkflow"
	// WorkItemRevisionSignalName delivers a newer revision to a running
	// work-item workflow. It is a signal, not a second workflow start, so the
	// running workflow keeps ownership of the change's one live child.
	WorkItemRevisionSignalName = "work-item-revision"

	// workItemPipelineVersion gates the durable intake claim and revision
	// signalling so histories recorded before those steps replay without them.
	workItemPipelineVersion = "workitem-pipeline-v1"

	// workItemPublicationVersion gates host-side publication and acceptance so
	// histories recorded before those steps replay without them.
	workItemPublicationVersion = "workitem-publication-v1"

	// workItemAcceptancePollVersion gates the bounded acceptance wait. A
	// history recorded with a single acceptance read must replay without the
	// poll timers it never scheduled.
	workItemAcceptancePollVersion = "workitem-acceptance-poll-v1"

	// workItemSignalDuringPublicationVersion gates the revision-signal receive
	// points that stay live through publication and the acceptance wait, and
	// the loop-top drain that follows a completed activation. A history
	// recorded before those receive points must replay on the direct
	// publication path: the signals it buffered were never consumed, so
	// consuming one would queue an activation the history does not contain.
	workItemSignalDuringPublicationVersion = "workitem-signal-during-publication-v1"

	// workItemAcceptancePollInterval is the workflow timer between forge reads.
	// The poll uses workflow time so replay re-creates the same timers.
	workItemAcceptancePollInterval = 15 * time.Minute

	// workItemAcceptanceMaxWait bounds how long one workflow waits for a
	// developer to merge or close the pull request.
	workItemAcceptanceMaxWait = 24 * time.Hour

	// Status values below are outcome names, not run states. They make a
	// publication failure distinguishable from a verification failure.
	workItemStatusPublicationFailed = "publication_failed"
	workItemStatusAcceptanceDrift   = "acceptance_drift"
	workItemStatusAcceptanceUnknown = "acceptance_unresolved"
	workItemStatusAcceptanceTimeout = "acceptance_timeout"
	workItemStatusWithdrawn         = "work_item_withdrawn"
	workItemStatusRework            = "rework"
	workItemStatusError             = "error"

	workItemWorkflowPrefix = "factory-workitem-"
	changeActivationPrefix = "factory-change-"
	workItemRunPrefix      = "wi-"
	workItemChangePrefix   = "workitem-"
	maxWorkItemTaskBytes   = 64 << 10
)

// WorkItemWorkflowInput is one intake delivery.
//
// Pipeline is optional: when empty the connector's unique operator pipeline is
// selected. The proposal carries the re-fetched work item, its exact-bytes
// digest, the forge-resolved actor, and the delivery identity. It never carries
// a repository selected by a payload or a forge credential.
type WorkItemWorkflowInput struct {
	Proposal forge.Proposal `json:"proposal"`
	// Pipeline optionally names an operator pipeline. An intake event usually
	// leaves it empty, and the connector's unique pipeline is resolved.
	Pipeline string `json:"pipeline,omitempty"`
}

// WorkItemRevisionSignal delivers a newer intake proposal to a running
// work-item workflow.
type WorkItemRevisionSignal struct {
	Proposal forge.Proposal `json:"proposal"`
}

// WorkItemRunRecord is one completed child activation in a workflow outcome.
type WorkItemRunRecord struct {
	Revision      string   `json:"revision"`
	RunID         string   `json:"run_id"`
	ChangeID      string   `json:"change_id"`
	WorkflowID    string   `json:"workflow_id"`
	FactoryResult RunState `json:"factory_result"`
}

// WorkItemOutcome is the durable result of one work-item workflow execution.
//
// Status is the last intake decision ("duplicate", "stale",
// "snapshot_conflict", ...) or the last child's factory result, so a caller can
// distinguish a rejected delivery from a run that completed badly.
type WorkItemOutcome struct {
	Connector   string               `json:"connector"`
	Repository  string               `json:"repository,omitempty"`
	WorkItemID  string               `json:"work_item_id"`
	Revision    string               `json:"revision"`
	ChangeID    string               `json:"change_id,omitempty"`
	Status      string               `json:"status"`
	Runs        []WorkItemRunRecord  `json:"runs,omitempty"`
	Conditions  []Condition          `json:"conditions,omitempty"`
	PullRequest *PullRequestIdentity `json:"pull_request,omitempty"`
	Acceptance  *AcceptanceRecord    `json:"acceptance,omitempty"`
}

// WorkItemResolveInput asks an activity to resolve an intake proposal against
// operator configuration and to claim the durable intake record.
type WorkItemResolveInput struct {
	Proposal forge.Proposal `json:"proposal"`
	Pipeline string         `json:"pipeline,omitempty"`
}

// WorkItemResolution is the activity's plain-data answer.
//
// It contains no clone or forge credential. The request is the exact child
// input the workflow will use; the workflow never re-reads configuration.
type WorkItemResolution struct {
	Accepted            bool       `json:"accepted"`
	Status              string     `json:"status"`
	Reason              string     `json:"reason,omitempty"`
	Connector           string     `json:"connector"`
	Pipeline            string     `json:"pipeline"`
	Repository          string     `json:"repository,omitempty"`
	BaseBranch          string     `json:"base_branch,omitempty"`
	Scope               string     `json:"scope,omitempty"`
	VerificationProfile string     `json:"verification_profile,omitempty"`
	AgentHarness        string     `json:"agent_harness,omitempty"`
	Task                string     `json:"task,omitempty"`
	TaskHash            string     `json:"task_hash,omitempty"`
	SnapshotHash        string     `json:"snapshot_hash,omitempty"`
	ChangeID            string     `json:"change_id,omitempty"`
	RunID               string     `json:"run_id,omitempty"`
	ParentRunID         string     `json:"parent_run_id,omitempty"`
	ActivationID        string     `json:"activation_id,omitempty"`
	Request             RunRequest `json:"request,omitempty"`
}

// WorkItemOutcomeInput records the child's result on the durable intake record.
type WorkItemOutcomeInput struct {
	Connector     string `json:"connector"`
	Repository    string `json:"repository"`
	WorkItemID    string `json:"work_item_id"`
	Revision      string `json:"revision"`
	RunID         string `json:"run_id"`
	Status        string `json:"status"`
	FactoryResult string `json:"factory_result,omitempty"`
	Error         string `json:"error,omitempty"`
}

// WorkItemWorkflowID derives the top-level workflow ID from the work item
// identity that is known before operator mapping: connector and work item.
//
// The revision is deliberately not part of the identity: one work item owns one
// running workflow, and a newer revision is signalled to it. Duplicate
// deliveries converge on the durable intake claim instead.
func WorkItemWorkflowID(connector, workItemID string) string {
	return workItemWorkflowPrefix + digestText(connector+"\x00"+workItemID, 32)
}

// LegacyWorkItemWorkflowID derives the revision-keyed workflow ID used before
// the work-item identity became stable. It remains for finding and cleaning up
// executions started under the old scheme.
func LegacyWorkItemWorkflowID(connector, workItemID, revision string) string {
	return workItemWorkflowPrefix + digestText(connector+"\x00"+workItemID+"\x00"+revision, 32)
}

// ChangeActivationWorkflowID derives the durable change activation workflow ID
// from connector, mapped repository, work item and revision. Duplicate starts
// are rejected by Temporal's workflow-ID reuse policy.
func ChangeActivationWorkflowID(connector, repository, workItemID, revision string) string {
	return changeActivationPrefix + digestText(strings.Join([]string{
		connector, repository, workItemID, revision,
	}, "\x00"), 32)
}

// WorkItemRunID derives the run identity for one work-item revision. It is
// stable so a retried start addresses the same artifact directory.
func WorkItemRunID(connector, repository, workItemID, revision string) string {
	return workItemRunPrefix + digestText(strings.Join([]string{
		connector, repository, workItemID, revision,
	}, "\x00"), 32)
}

// WorkItemChangeID derives the durable Change identity for one work item.
// Revisions share it, which is what makes rework line up instead of creating a
// new change per delivery.
func WorkItemChangeID(connector, repository, workItemID string) string {
	return workItemChangePrefix + digestText(strings.Join([]string{
		connector, repository, workItemID,
	}, "\x00"), 32)
}

// WorkItemWorkflow is the durable pipeline top level:
//
//	resolve (activity) → claim intake → SoftwareChangeWorkflow (child)
//	                                   → signal newer revisions
//	                                   → repeat for each pending revision
//
// It never reads operator configuration, a forge client, or wall-clock time.
// Every mapping and resource decision arrives from the resolution activity as
// plain data, and a newer revision is received as a signal rather than a second
// concurrent child.
func WorkItemWorkflow(ctx workflow.Context, in WorkItemWorkflowInput) (WorkItemOutcome, error) {
	acts := &Activities{}
	version := workflow.GetVersion(ctx, workItemPipelineVersion, workflow.DefaultVersion, 1)
	// This marker is called before any of the receive points it gates so a
	// pre-marker history replays its original command order from the start and
	// can never reach a signal select it never recorded.
	signalDuringPublication := workflow.GetVersion(ctx, workItemSignalDuringPublicationVersion,
		workflow.DefaultVersion, 1) != workflow.DefaultVersion
	signals := workflow.GetSignalChannel(ctx, WorkItemRevisionSignalName)

	outcome := WorkItemOutcome{
		Connector:  strings.TrimSpace(in.Proposal.Connector),
		WorkItemID: strings.TrimSpace(in.Proposal.WorkItem.ID),
		Revision:   strings.TrimSpace(in.Proposal.WorkItem.Revision),
	}
	pending := []forge.Proposal{in.Proposal}

	for {
		// A signal that arrived after the last receive point stays buffered
		// until the workflow ends; take it now instead of finishing with an
		// unread revision. A history recorded before the signal-during-
		// publication marker never had this receive point and must keep its
		// original loop exit.
		if len(pending) == 0 {
			if signalDuringPublication {
				drainWorkItemSignals(signals, &pending, outcome.Revision)
			}
			if len(pending) == 0 {
				break
			}
		}

		proposal := pending[0]
		pending = pending[1:]

		resolved, err := resolveWorkItemIntake(ctx, acts, proposal, in.Pipeline)
		if err != nil {
			setWorkItemCondition(ctx, &outcome.Conditions, ConditionIntake, ConditionFalse, "ResolveFailed", err.Error())
			outcome.Status = workItemStatusError
			return finishWorkItemOutcome(ctx, outcome), nil
		}
		outcome.Connector = resolved.Connector
		outcome.Repository = resolved.Repository
		outcome.WorkItemID = strings.TrimSpace(proposal.WorkItem.ID)
		outcome.Revision = strings.TrimSpace(proposal.WorkItem.Revision)
		if resolved.ChangeID != "" {
			outcome.ChangeID = resolved.ChangeID
		}
		if !resolved.Accepted {
			setWorkItemCondition(ctx, &outcome.Conditions, ConditionIntake, ConditionFalse,
				intakeConditionReason(resolved.Status), resolved.Reason)
			outcome.Status = resolved.Status
			continue
		}

		setWorkItemCondition(ctx, &outcome.Conditions, ConditionIntake, ConditionTrue, "Accepted",
			"pipeline="+resolved.Pipeline+" repository="+resolved.Repository)
		outcome.Status = intakeStatusRunning

		manifest, childErr := runWorkItemChild(ctx, resolved, proposal.WorkItem.Revision, version, signals, &pending)
		if childErr != nil {
			if temporal.IsWorkflowExecutionAlreadyStartedError(childErr) {
				setWorkItemCondition(ctx, &outcome.Conditions, ConditionIntake, ConditionFalse, "DuplicateStart",
					"the change activation already exists")
				outcome.Status = intakeStatusDuplicate
				recordWorkItemOutcome(ctx, acts, WorkItemOutcomeInput{
					Connector: resolved.Connector, Repository: resolved.Repository,
					WorkItemID: proposal.WorkItem.ID, Revision: proposal.WorkItem.Revision,
					RunID: resolved.RunID, Status: intakeStatusDuplicate,
				})
				continue
			}
			// The intake decision was accepted; the child failed afterward, so
			// the Intake condition stays True and the outcome names the error.
			outcome.Status = workItemStatusError
			recordWorkItemOutcome(ctx, acts, WorkItemOutcomeInput{
				Connector: resolved.Connector, Repository: resolved.Repository,
				WorkItemID: proposal.WorkItem.ID, Revision: proposal.WorkItem.Revision,
				RunID: resolved.RunID, Status: intakeRecordError, Error: childErr.Error(),
			})
			continue
		}

		outcome.Runs = append(outcome.Runs, WorkItemRunRecord{
			Revision:      proposal.WorkItem.Revision,
			RunID:         resolved.RunID,
			ChangeID:      resolved.ChangeID,
			WorkflowID:    resolved.ActivationID,
			FactoryResult: manifest.FactoryResult,
		})
		outcome.Status = string(manifest.FactoryResult)
		recordStatus := intakeRecordFailed
		if manifest.FactoryResult == StateSucceeded {
			recordStatus = intakeRecordSucceeded
			if workflow.GetVersion(ctx, workItemPublicationVersion, workflow.DefaultVersion, 1) != workflow.DefaultVersion {
				var (
					publication    PublishChangeOutput
					acceptance     *ReadPublicationAcceptanceOutput
					publicationErr error
				)
				if signalDuringPublication {
					publication, acceptance, publicationErr = publishAndReadAcceptanceKeepingSignals(
						ctx, acts, manifest, &outcome, proposal.WorkItem.Revision, signals, &pending)
				} else {
					// A history recorded before the marker had no receive point
					// here; keep its direct publication path.
					publication, acceptance, publicationErr = publishAndReadAcceptance(ctx, acts, manifest, &outcome)
				}
				if publicationErr != nil {
					workflow.GetLogger(ctx).Warn("publication step failed",
						"run_id", resolved.RunID, "error", publicationErr)
				}
				switch {
				case publication.Withdrawn:
					outcome.Status = workItemStatusWithdrawn
					recordStatus = intakeRecordWithdrawn
				case publication.Failure != "":
					outcome.Status = workItemStatusPublicationFailed
					if publication.GateFailed {
						outcome.Status = string(StateGateTampered)
					}
					recordStatus = intakeRecordFailed
				case publicationErr != nil && outcome.PullRequest == nil:
					outcome.Status = workItemStatusPublicationFailed
					recordStatus = intakeRecordFailed
				case acceptance != nil && acceptance.TimedOut:
					outcome.Status = workItemStatusAcceptanceTimeout
					if !acceptance.Read {
						outcome.Status = workItemStatusAcceptanceUnknown
					}
				case acceptance != nil && acceptance.Drift:
					outcome.Status = workItemStatusAcceptanceDrift
					recordStatus = intakeRecordFailed
				case acceptance != nil && acceptance.Decision == string(publicationDecisionClosedService):
					outcome.Status = workItemStatusAcceptanceUnknown
				case acceptance != nil && acceptance.Decision == string(publicationDecisionClosedHuman):
					outcome.Status = workItemStatusRework
				}
			}
		}
		recordWorkItemOutcome(ctx, acts, WorkItemOutcomeInput{
			Connector: resolved.Connector, Repository: resolved.Repository,
			WorkItemID: proposal.WorkItem.ID, Revision: proposal.WorkItem.Revision,
			RunID: resolved.RunID, Status: recordStatus,
			FactoryResult: string(manifest.FactoryResult),
		})
	}

	return finishWorkItemOutcome(ctx, outcome), nil
}

// resolveWorkItemIntake runs the resolution activity. It is split out so a
// failed resolution has the same condition recording as any other intake path.
func resolveWorkItemIntake(
	ctx workflow.Context,
	acts *Activities,
	proposal forge.Proposal,
	pipeline string,
) (WorkItemResolution, error) {
	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		// Resolution is deterministic against operator configuration and the
		// durable claim is idempotent by revision and digest, so a retry is safe.
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	}
	var resolved WorkItemResolution
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), acts.ResolveWorkItemIntake,
		WorkItemResolveInput{Proposal: proposal, Pipeline: pipeline}).Get(ctx, &resolved)
	return resolved, err
}

// workItemChildOptions is the child start contract for one pipeline activation.
//
// The child runs under the standard run workflow identity so the console, the
// live status lookup, and the halt/cancel controls address it like any other
// run; under the activation identity they discard it as an unknown workflow.
// The memo is the same summary a directly started run carries, so a filtered
// halt sees the pipeline child too. ActivationID remains the durable intake
// claim and audit identity, not a workflow identity.
func workItemChildOptions(resolved WorkItemResolution) workflow.ChildWorkflowOptions {
	workflowID := resolved.ActivationID
	if runID := strings.TrimSpace(resolved.Request.RunID); runID != "" {
		workflowID = WorkflowIDForRun(runID)
	}
	// An accepted resolution always carries a run ID. Without one the child
	// keeps the activation identity rather than an identity no control reads.
	return workflow.ChildWorkflowOptions{
		WorkflowID:            workflowID,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WaitForCancellation:   true,
		Memo:                  RunMemoFor(resolved.Request),
	}
}

// runWorkItemChild starts the existing change workflow as a child and consumes
// revision signals while it runs.
//
// A newer revision is appended to pending rather than starting a second child,
// so one sandbox is live at a time and the next revision starts only after the
// current activation has cleaned up.
func runWorkItemChild(
	ctx workflow.Context,
	resolved WorkItemResolution,
	currentRevision string,
	version workflow.Version,
	signals workflow.ReceiveChannel,
	pending *[]forge.Proposal,
) (RunManifest, error) {
	opts := workItemChildOptions(resolved)
	childCtx := workflow.WithChildOptions(ctx, opts)
	future := workflow.ExecuteChildWorkflow(childCtx, SoftwareChangeWorkflow, resolved.Request)

	var manifest RunManifest
	if version == workflow.DefaultVersion {
		// A history recorded before the pipeline marker has no signal path.
		if err := future.Get(ctx, &manifest); err != nil {
			return manifest, err
		}
		return manifest, nil
	}

	var childErr error
	done := false
	for !done {
		selector := workflow.NewSelector(ctx)
		selector.AddFuture(future, func(f workflow.Future) {
			childErr = f.Get(ctx, &manifest)
			done = true
		})
		selector.AddReceive(signals, func(ch workflow.ReceiveChannel, _ bool) {
			var signal WorkItemRevisionSignal
			ch.Receive(ctx, &signal)
			queueWorkItemSignal(pending, currentRevision, signal)
		})
		selector.Select(ctx)
	}
	// A signal that arrived in the same workflow task as the child completion
	// must not be lost between the selector and the loop.
	drainWorkItemSignals(signals, pending, currentRevision)
	return manifest, childErr
}

// queueWorkItemSignal appends a well-formed proposal that is newer than every
// revision already accepted or pending.
//
// At-least-once reconciliation replays a delivered revision. Queueing that
// replay would re-resolve the durable intake record as a duplicate and could
// replace a completed outcome with it, so a revision that is not newer than the
// current or pending one is ignored.
func queueWorkItemSignal(pending *[]forge.Proposal, currentRevision string, signal WorkItemRevisionSignal) {
	revision := strings.TrimSpace(signal.Proposal.WorkItem.Revision)
	if strings.TrimSpace(signal.Proposal.WorkItem.ID) == "" || revision == "" {
		return
	}
	latest := strings.TrimSpace(currentRevision)
	for _, queued := range *pending {
		if compareWorkItemRevisions(queued.WorkItem.Revision, latest) > 0 {
			latest = queued.WorkItem.Revision
		}
	}
	if compareWorkItemRevisions(revision, latest) <= 0 {
		return
	}
	*pending = append(*pending, signal.Proposal)
}

// drainWorkItemSignals consumes buffered revision signals without blocking.
func drainWorkItemSignals(signals workflow.ReceiveChannel, pending *[]forge.Proposal, currentRevision string) {
	for {
		var signal WorkItemRevisionSignal
		if !signals.ReceiveAsync(&signal) {
			return
		}
		queueWorkItemSignal(pending, currentRevision, signal)
	}
}

// publishAndReadAcceptance runs the host-side publication, then observes the
// forge merge state with a bounded workflow-timer poll. The wait is durable and
// replay-safe: the same timers are scheduled on replay, and a history recorded
// before the poll version keeps its single read.
func publishAndReadAcceptance(
	ctx workflow.Context,
	acts *Activities,
	manifest RunManifest,
	outcome *WorkItemOutcome,
) (PublishChangeOutput, *ReadPublicationAcceptanceOutput, error) {
	pollAcceptance := workflow.GetVersion(ctx, workItemAcceptancePollVersion, workflow.DefaultVersion, 1) != workflow.DefaultVersion
	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		// Publication is idempotent by branch and pull request. The forge
		// client already honours 429 retry hints, so a bounded retry is enough.
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	}
	var publication PublishChangeOutput
	publicationErr := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), acts.PublishChange,
		PublishChangeInput{RunID: manifest.RunID}).Get(ctx, &publication)
	if publicationErr != nil {
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionPublication, ConditionFalse, "PublishError", publicationErr.Error())
		return PublishChangeOutput{}, nil, publicationErr
	}
	switch {
	case publication.Withdrawn:
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionPublication, ConditionFalse, "Withdrawn",
			publication.Failure)
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "Withdrawn",
			"the work item was withdrawn before publication")
		return publication, nil, nil
	case publication.Disabled:
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionPublication, ConditionUnknown, "NotConfigured",
			"evidence signing is not configured, so publication is disabled")
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "NotConfigured",
			"publication did not run")
		return publication, nil, nil
	case publication.EmptyPatch:
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionPublication, ConditionUnknown, "EmptyPatch",
			"the patch is empty; no pull request was opened")
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "NotApplicable",
			"there is no pull request to accept")
		return publication, nil, nil
	case publication.Failure != "":
		reason := publication.FailureClass
		if reason == "" {
			reason = "PublishFailed"
		}
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionPublication, ConditionFalse, reason, publication.Failure)
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "NotPublished",
			"no pull request was published")
		return publication, nil, nil
	}
	outcome.PullRequest = &PullRequestIdentity{
		ID: publication.PullRequestID, URL: publication.PullRequestURL,
		Repository: manifest.Repository, Branch: publication.Branch,
		Commit: publication.Commit, Tree: publication.Tree,
		BundleDigest: publication.BundleDigest,
	}
	setWorkItemCondition(ctx, &outcome.Conditions, ConditionPublication, ConditionTrue, "Published",
		"pull request "+publication.PullRequestID+" branch "+publication.Branch)

	acceptanceOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	}
	acceptance, err := readAcceptanceBounded(ctx, acts, acceptanceOpts, manifest.RunID, pollAcceptance)
	if err != nil {
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "ReadFailed", err.Error())
		return publication, nil, err
	}
	outcome.Acceptance = acceptance.Acceptance
	if acceptance.TimedOut {
		message := "the pull request stayed open for the maximum acceptance wait"
		if acceptance.Note != "" {
			message = acceptance.Note
		}
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "TimedOut", message)
		return publication, &acceptance, nil
	}
	if !acceptance.Read {
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "NoPullRequest",
			"the forge did not report a pull request to observe")
		return publication, &acceptance, nil
	}
	switch acceptance.Decision {
	case string(publicationDecisionAccepted), string(publicationDecisionAlreadyApplied):
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionTrue, "Merged",
			"merge commit "+acceptance.MergeCommit)
	case string(publicationDecisionClosedHuman):
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionFalse, "Closed", acceptance.Note)
	case string(publicationDecisionClosedService):
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionFalse, "ServiceClosed", acceptance.Note)
	case string(publicationDecisionDrift):
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionFalse, "Drift", acceptance.Note)
	case string(publicationDecisionIgnoredStale):
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "Superseded",
			"the pull request was superseded before acceptance")
	case string(publicationDecisionPending):
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "PendingPublication", acceptance.Note)
	default:
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "Open",
			"the pull request is still open")
	}
	return publication, &acceptance, nil
}

// publishAndReadAcceptanceKeepingSignals runs publication and the acceptance
// wait while the revision signal channel stays selectable.
//
// The phase runs in a workflow goroutine and this coroutine selects over its
// result and the signal channel, applying the same revision-ordering filter as
// the child selector. A newer revision delivered during publication or the
// acceptance wait is therefore queued for another activation instead of being
// discarded when the workflow ends. Signals delivered in the same workflow
// task as the phase result are drained before returning. The caller reaches
// this path only when the workItemSignalDuringPublicationVersion marker is
// present.
func publishAndReadAcceptanceKeepingSignals(
	ctx workflow.Context,
	acts *Activities,
	manifest RunManifest,
	outcome *WorkItemOutcome,
	currentRevision string,
	signals workflow.ReceiveChannel,
	pending *[]forge.Proposal,
) (PublishChangeOutput, *ReadPublicationAcceptanceOutput, error) {
	type phaseResult struct {
		publication PublishChangeOutput
		acceptance  *ReadPublicationAcceptanceOutput
		err         error
	}
	done := workflow.NewChannel(ctx)
	workflow.Go(ctx, func(ctx workflow.Context) {
		publication, acceptance, err := publishAndReadAcceptance(ctx, acts, manifest, outcome)
		done.Send(ctx, phaseResult{publication: publication, acceptance: acceptance, err: err})
	})

	var result phaseResult
	for received := false; !received; {
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(done, func(ch workflow.ReceiveChannel, _ bool) {
			ch.Receive(ctx, &result)
			received = true
		})
		selector.AddReceive(signals, func(ch workflow.ReceiveChannel, _ bool) {
			var signal WorkItemRevisionSignal
			ch.Receive(ctx, &signal)
			queueWorkItemSignal(pending, currentRevision, signal)
		})
		selector.Select(ctx)
	}
	// A signal buffered in the same workflow task as the phase result must not
	// be lost between the selector and the caller's next decision.
	drainWorkItemSignals(signals, pending, currentRevision)
	return result.publication, result.acceptance, result.err
}

// readAcceptanceBounded polls the forge merge state on workflow timers until a
// terminal decision or the maximum acceptance wait. It is deterministic: every
// wait is a workflow timer, never a wall-clock sleep.
func readAcceptanceBounded(
	ctx workflow.Context,
	acts *Activities,
	opts workflow.ActivityOptions,
	runID string,
	poll bool,
) (ReadPublicationAcceptanceOutput, error) {
	read := func() (ReadPublicationAcceptanceOutput, error) {
		var out ReadPublicationAcceptanceOutput
		err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), acts.ReadPublicationAcceptance,
			ReadPublicationAcceptanceInput{RunID: runID}).Get(ctx, &out)
		return out, err
	}
	out, readErr := read()
	if !poll {
		return out, readErr
	}
	deadline := workflow.Now(ctx).Add(workItemAcceptanceMaxWait)
	for {
		if readErr == nil && (acceptanceTerminal(out) || out.TimedOut) {
			return out, nil
		}
		if !workflow.Now(ctx).Before(deadline) {
			out.TimedOut = true
			if readErr != nil {
				out.Note = fmt.Sprintf("the acceptance state could not be read before the deadline: %v", readErr)
			}
			return out, nil
		}
		if err := workflow.NewTimer(ctx, workItemAcceptancePollInterval).Get(ctx, nil); err != nil {
			return out, err
		}
		next, err := read()
		if err != nil {
			// A transient forge read must not end the wait: keep the last
			// successful output and poll again until the deadline.
			readErr = err
			continue
		}
		out = next
		readErr = nil
	}
}

// acceptanceTerminal reports whether a read ends the acceptance wait.
func acceptanceTerminal(out ReadPublicationAcceptanceOutput) bool {
	if !out.Read {
		return true
	}
	switch out.Decision {
	case string(publicationDecisionAccepted),
		string(publicationDecisionAlreadyApplied),
		string(publicationDecisionClosedHuman),
		string(publicationDecisionClosedService),
		string(publicationDecisionDrift),
		string(publicationDecisionIgnoredStale):
		return true
	default:
		return false
	}
}

// finishWorkItemOutcome records the publication and acceptance conditions that
// this workflow did not reach. They are Unknown rather than True so a consumer
// can never read "no publication step ran" as a published change.
func finishWorkItemOutcome(ctx workflow.Context, outcome WorkItemOutcome) WorkItemOutcome {
	if _, ok := ConditionFor(outcome.Conditions, ConditionPublication); !ok {
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionPublication, ConditionUnknown, "NotRun",
			"no publication step ran")
	}
	if _, ok := ConditionFor(outcome.Conditions, ConditionAcceptance); !ok {
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "NotRun",
			"no acceptance was read")
	}
	return outcome
}

func setWorkItemCondition(ctx workflow.Context, conditions *[]Condition, t ConditionType, status ConditionStatus, reason, message string) {
	*conditions = SetCondition(*conditions, Condition{
		Type:               t,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: workflow.Now(ctx),
	})
}

func intakeConditionReason(status string) string {
	switch status {
	case intakeStatusDuplicate:
		return "Duplicate"
	case intakeStatusStale:
		return "Stale"
	case intakeStatusConflict:
		return "SnapshotChanged"
	case intakeStatusNoMapping:
		return "NoMapping"
	case intakeStatusScopeAmbiguous:
		return "ScopeAmbiguous"
	case intakeStatusRejected:
		return "Rejected"
	default:
		return "NotAccepted"
	}
}

func recordWorkItemOutcome(ctx workflow.Context, acts *Activities, in WorkItemOutcomeInput) {
	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumAttempts:    3,
		},
	}
	if err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, opts), acts.RecordWorkItemOutcome, in).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("work item intake outcome not recorded",
			"run_id", in.RunID, "revision", in.Revision, "error", err)
	}
}

func digestText(value string, hexChars int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	if hexChars > 0 && hexChars < len(encoded) {
		return encoded[:hexChars]
	}
	return encoded
}

// compareWorkItemRevisions orders forge revisions. Azure DevOps uses positive
// integers; a non-numeric revision falls back to a stable lexical order rather
// than being treated as always newer.
func compareWorkItemRevisions(a, b string) int {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	ai, aErr := strconv.ParseInt(a, 10, 64)
	bi, bErr := strconv.ParseInt(b, 10, 64)
	if aErr == nil && bErr == nil {
		switch {
		case ai < bi:
			return -1
		case ai > bi:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(a, b)
}
