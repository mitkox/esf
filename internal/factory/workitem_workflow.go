package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/repository"
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

	workItemWorkflowPrefix     = "factory-workitem-"
	changeActivationPrefix     = "factory-change-"
	workItemRunPrefix          = "wi-"
	workItemChangePrefix       = "workitem-"
	intakeDirName              = "intake"
	maxWorkItemTaskBytes       = 64 << 10
	maxIntakeFieldBytes        = 4096
	intakeStateFileName        = "state.json"
	intakeAuditFileName        = "audit.jsonl"
	intakeStateLockFileName    = "state.lock"
	intakeRecordAccepted       = "accepted"
	intakeRecordSucceeded      = "succeeded"
	intakeRecordFailed         = "failed"
	intakeRecordWithdrawn      = "withdrawn"
	intakeRecordError          = "error"
	intakeStatusDuplicate      = "duplicate"
	intakeStatusStale          = "stale"
	intakeStatusConflict       = "snapshot_conflict"
	intakeStatusNoMapping      = "no_mapping"
	intakeStatusScopeAmbiguous = "scope_ambiguous"
	intakeStatusRejected       = "rejected"
	intakeStatusRunning        = "running"
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
	signals := workflow.GetSignalChannel(ctx, WorkItemRevisionSignalName)

	outcome := WorkItemOutcome{
		Connector:  strings.TrimSpace(in.Proposal.Connector),
		WorkItemID: strings.TrimSpace(in.Proposal.WorkItem.ID),
		Revision:   strings.TrimSpace(in.Proposal.WorkItem.Revision),
	}
	pending := []forge.Proposal{in.Proposal}

	for len(pending) > 0 {
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
				publication, acceptance, publicationErr := publishAndReadAcceptance(ctx, acts, manifest, &outcome)
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
	opts := workflow.ChildWorkflowOptions{
		WorkflowID:            resolved.ActivationID,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WaitForCancellation:   true,
	}
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
	if !acceptance.Read {
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "NoPullRequest",
			"the forge did not report a pull request to observe")
		return publication, &acceptance, nil
	}
	outcome.Acceptance = acceptance.Acceptance
	if acceptance.TimedOut {
		setWorkItemCondition(ctx, &outcome.Conditions, ConditionAcceptance, ConditionUnknown, "TimedOut",
			"the pull request stayed open for the maximum acceptance wait")
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
	first, err := read()
	if err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	if !poll {
		return first, nil
	}
	deadline := workflow.Now(ctx).Add(workItemAcceptanceMaxWait)
	out := first
	for {
		if acceptanceTerminal(out) || out.TimedOut {
			return out, nil
		}
		if !workflow.Now(ctx).Before(deadline) {
			out.TimedOut = true
			return out, nil
		}
		if err := workflow.NewTimer(ctx, workItemAcceptancePollInterval).Get(ctx, nil); err != nil {
			return out, err
		}
		out, err = read()
		if err != nil {
			return ReadPublicationAcceptanceOutput{}, err
		}
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

// workItemRunRequest is the pipeline's in-process use of the shared submission
// contract. The pipeline resolves the harness and verification profile for the
// work item's scope and passes them in, so the shared step leaves those values
// in place. The work item, forge connector and review override describe the
// pipeline origin; they are added here rather than to the shared contract.
func workItemRunRequest(cfg Config, sub Submission, workItem *WorkItemEvidence, connector string, review bool) (RunRequest, error) {
	run, err := PrepareChangeRun(cfg, sub)
	if err != nil {
		return RunRequest{}, err
	}
	run.WorkItem = workItem
	run.ForgeConnector = connector
	run.Review = &review
	return run, nil
}

// ResolveWorkItemIntake resolves operator mapping and resources, then claims
// the durable intake record before any Change exists.
func (a *Activities) ResolveWorkItemIntake(ctx context.Context, in WorkItemResolveInput) (WorkItemResolution, error) {
	proposal := in.Proposal
	connector := strings.TrimSpace(proposal.Connector)
	if connector == "" {
		return WorkItemResolution{}, fmt.Errorf("work item intake: connector is required")
	}
	workItem := proposal.WorkItem
	if strings.TrimSpace(workItem.ID) == "" || strings.TrimSpace(workItem.Revision) == "" {
		return WorkItemResolution{}, fmt.Errorf("work item intake: work item id and revision are required")
	}
	if strings.TrimSpace(proposal.WorkItemHash) == "" {
		return WorkItemResolution{}, fmt.Errorf("work item intake: snapshot digest is required")
	}
	actor := proposal.Actor
	if strings.TrimSpace(actor.Descriptor) == "" {
		return WorkItemResolution{}, fmt.Errorf("work item intake: actor identity was not resolved through the forge")
	}

	fields := workItem.MappingFields()
	scope, err := a.resolveScope(connector, in.Pipeline, fields)
	if err != nil {
		status := intakeStatusNoMapping
		var ambiguous *AmbiguousScopeError
		if errors.As(err, &ambiguous) {
			status = intakeStatusScopeAmbiguous
		}
		resolution := WorkItemResolution{Status: status, Reason: err.Error(), Connector: connector}
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}

	resolved, err := a.resolvePipeline(connector, in.Pipeline, fields)
	if err != nil {
		status := intakeStatusNoMapping
		var ambiguous *AmbiguousScopeError
		if errors.As(err, &ambiguous) {
			status = intakeStatusScopeAmbiguous
		}
		resolution := WorkItemResolution{Status: status, Reason: err.Error(), Connector: connector}
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}
	if resolved.Scope != scope {
		resolution := WorkItemResolution{
			Status: intakeStatusRejected, Connector: connector,
			Reason: fmt.Sprintf("scope resolution disagreed: %q resolved from the work item, %q from the pipeline",
				scope, resolved.Scope),
		}
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}

	resolution := WorkItemResolution{
		Connector:           connector,
		Pipeline:            resolved.Pipeline,
		Repository:          resolved.Repository,
		BaseBranch:          resolved.BaseBranch,
		Scope:               scope,
		VerificationProfile: resolved.VerificationProfile,
		SnapshotHash:        proposal.WorkItemHash,
	}
	task := workItemTask(workItem)
	if task == "" {
		resolution.Status = intakeStatusRejected
		resolution.Reason = "the work item snapshot is empty"
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}
	harness, err := a.defaultHarness(scope)
	if err != nil {
		resolution.Status = intakeStatusRejected
		resolution.Reason = err.Error()
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}
	// The run identity is derived before submission so the shared constructor
	// validates the exact identifier the workflow will use.
	resolution.AgentHarness = harness
	resolution.Task = task
	resolution.TaskHash = repository.HashTask(task)
	resolution.ChangeID = WorkItemChangeID(connector, resolved.Repository, workItem.ID)
	resolution.RunID = WorkItemRunID(connector, resolved.Repository, workItem.ID, workItem.Revision)
	resolution.ActivationID = ChangeActivationWorkflowID(connector, resolved.Repository, workItem.ID, workItem.Revision)

	// Pipeline runs use the forge pull request as acceptance. The pre-publication
	// human gate is for CLI-driven runs; enabling it here would make a gate
	// timeout look like forge acceptance.
	request, err := workItemRunRequest(a.cfg, Submission{
		RunID:               resolution.RunID,
		Repository:          resolved.Repository,
		Revision:            resolved.BaseBranch,
		Task:                task,
		AgentHarness:        harness,
		VerificationProfile: resolved.VerificationProfile,
		Scope:               scope,
		ChangeID:            resolution.ChangeID,
	}, &WorkItemEvidence{
		Connector:    connector,
		Pipeline:     resolved.Pipeline,
		ID:           workItem.ID,
		Revision:     workItem.Revision,
		Project:      workItem.Project,
		State:        workItem.State,
		SnapshotHash: proposal.WorkItemHash,
		Repository:   resolved.Repository,
		Source:       string(proposal.Source),
		Actor:        actorEvidenceFor(actor, proposal.RepositoryPermission),
	}, connector, false)
	if err != nil {
		resolution.Status = intakeStatusRejected
		resolution.Reason = err.Error()
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}

	resources, err := a.cfg.ResolveResources(request)
	if err != nil {
		resolution.Status = intakeStatusRejected
		resolution.Reason = err.Error()
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}
	if err := a.repos.Validate(repository.Request{
		Kind: repository.SourceRemote, URL: resolved.Repository, Revision: resolved.BaseBranch,
	}); err != nil {
		resolution.Status = intakeStatusRejected
		resolution.Reason = err.Error()
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}
	if !matchesAnyPrefix(resolved.Repository, resources.RepositoriesAllowed) {
		resolution.Status = intakeStatusRejected
		resolution.Reason = fmt.Sprintf("repository %q is not allowed in scope %q", resolved.Repository, resources.Scope)
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
		return resolution, nil
	}
	request.SandboxTemplate = resources.WorkspaceConfig.Template
	request.AgentHarness = harness
	request.Scope = resources.Scope
	if resources.VerificationProfile != "" {
		request.VerificationProfile = resources.VerificationProfile
	}
	resolution.Scope = resources.Scope
	resolution.VerificationProfile = request.VerificationProfile
	resolution.Request = request

	store, err := openIntakeStore(a.artifactFactory.Root())
	if err != nil {
		return WorkItemResolution{}, err
	}
	claim, err := store.Claim(ctx, intakeRecord{
		Connector:           connector,
		Pipeline:            resolved.Pipeline,
		Repository:          resolved.Repository,
		WorkItemID:          workItem.ID,
		Revision:            workItem.Revision,
		SnapshotHash:        proposal.WorkItemHash,
		Source:              string(proposal.Source),
		DeliveryID:          proposal.DeliveryID,
		ActorDescriptor:     actor.Descriptor,
		ActorPermission:     proposal.RepositoryPermission,
		ChangeID:            resolution.ChangeID,
		RunID:               resolution.RunID,
		ActivationID:        resolution.ActivationID,
		BaseBranch:          resolved.BaseBranch,
		VerificationProfile: request.VerificationProfile,
	})
	if err != nil {
		return WorkItemResolution{}, err
	}
	resolution.Accepted = claim.Accepted
	resolution.Status = claim.Status
	resolution.ParentRunID = claim.Record.ParentRunID
	resolution.Reason = claim.Reason
	request.ParentRunID = resolution.ParentRunID
	resolution.Request = request

	if claim.Accepted {
		auditIntake(ctx, a, proposal, resolution, "SUCCESS")
	} else if claim.Status == intakeStatusDuplicate {
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeDuplicate)
	} else {
		auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
	}
	return resolution, nil
}

func (a *Activities) resolvePipeline(connector, pipeline string, fields forge.WorkItemFields) (ResolvedPipeline, error) {
	if name := strings.TrimSpace(pipeline); name != "" {
		return a.cfg.ResolvePipeline(name, fields)
	}
	return a.cfg.ResolvePipelineForConnector(connector, fields)
}

// resolveScope resolves the one operator scope a work item belongs to. The
// pipeline lookup and the scope lookup use the same match rules, and the caller
// rejects the work item when the two do not agree.
func (a *Activities) resolveScope(connector, pipeline string, fields forge.WorkItemFields) (string, error) {
	if name := strings.TrimSpace(pipeline); name != "" {
		return a.cfg.ResolveScope(name, fields)
	}
	return a.cfg.ResolveScopeForConnector(connector, fields)
}

func (a *Activities) defaultHarness(scope string) (string, error) {
	return a.cfg.DefaultHarness(scope, a.harnesses.Names())
}

// RecordWorkItemOutcome folds the child result into the durable intake record.
func (a *Activities) RecordWorkItemOutcome(ctx context.Context, in WorkItemOutcomeInput) error {
	store, err := openIntakeStore(a.artifactFactory.Root())
	if err != nil {
		return err
	}
	return store.RecordOutcome(ctx, in)
}

// workItemTask composes the bounded task text from the re-fetched work item.
//
// It is deliberately built from forge content and not from an operator prompt:
// the task artifact and TaskHash then describe exactly the bytes the agent was
// given.
func workItemTask(item forge.WorkItem) string {
	title := strings.TrimSpace(item.Title)
	body := strings.TrimSpace(item.Body)
	text := title
	if body != "" {
		if text != "" {
			text += "\n\n"
		}
		text += body
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\x00", "")
	return truncateUTF8Bytes(strings.TrimSpace(text), maxWorkItemTaskBytes)
}

func truncateUTF8Bytes(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && (value[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}

func digestText(value string, hexChars int) string {
	sum := sha256.Sum256([]byte(value))
	encoded := hex.EncodeToString(sum[:])
	if hexChars > 0 && hexChars < len(encoded) {
		return encoded[:hexChars]
	}
	return encoded
}

// ── Durable intake records ───────────────────────────────────────────────────

// intakeRecord is one durable intake decision. Its identity is the mapped
// repository plus work-item revision, so a webhook and a sweep converge.
type intakeRecord struct {
	Connector           string    `json:"connector"`
	Pipeline            string    `json:"pipeline,omitempty"`
	Repository          string    `json:"repository"`
	WorkItemID          string    `json:"work_item_id"`
	Revision            string    `json:"revision"`
	SnapshotHash        string    `json:"snapshot_hash"`
	Source              string    `json:"source,omitempty"`
	DeliveryID          string    `json:"delivery_id,omitempty"`
	ActorDescriptor     string    `json:"actor_descriptor,omitempty"`
	ActorPermission     string    `json:"actor_permission,omitempty"`
	ChangeID            string    `json:"change_id"`
	RunID               string    `json:"run_id"`
	ActivationID        string    `json:"activation_id"`
	ParentRunID         string    `json:"parent_run_id,omitempty"`
	BaseBranch          string    `json:"base_branch,omitempty"`
	VerificationProfile string    `json:"verification_profile,omitempty"`
	Status              string    `json:"status"`
	FactoryResult       string    `json:"factory_result,omitempty"`
	Error               string    `json:"error,omitempty"`
	RecordedAt          time.Time `json:"recorded_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type intakeHead struct {
	Connector      string    `json:"connector"`
	Repository     string    `json:"repository"`
	WorkItemID     string    `json:"work_item_id"`
	LatestRevision string    `json:"latest_revision"`
	SnapshotHash   string    `json:"snapshot_hash"`
	ChangeID       string    `json:"change_id"`
	RunID          string    `json:"run_id"`
	ActivationID   string    `json:"activation_id"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type intakeState struct {
	Heads   map[string]intakeHead   `json:"heads"`
	Records map[string]intakeRecord `json:"records"`
}

type intakeClaim struct {
	Accepted bool
	Status   string
	Reason   string
	Record   intakeRecord
}

type intakeStore struct {
	root string
}

func openIntakeStore(artifactRoot string) (*intakeStore, error) {
	if strings.TrimSpace(artifactRoot) == "" {
		return nil, errors.New("intake store: artifact root is required")
	}
	root := filepath.Join(artifactRoot, intakeDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("intake store: create %s: %w", root, err)
	}
	return &intakeStore{root: root}, nil
}

func (s *intakeStore) Claim(ctx context.Context, rec intakeRecord) (intakeClaim, error) {
	if err := ctx.Err(); err != nil {
		return intakeClaim{}, err
	}
	if err := validateIntakeRecord(rec); err != nil {
		return intakeClaim{}, err
	}
	now := time.Now().UTC()
	rec.RecordedAt = now
	rec.UpdatedAt = now
	rec.Status = intakeRecordAccepted
	rec.ActorDescriptor = strings.TrimSpace(rec.ActorDescriptor)

	var claim intakeClaim
	err := s.withState(func(state *intakeState) error {
		recordKey := intakeRecordKey(rec)
		headKey := intakeHeadKey(rec)
		head, hasHead := state.Heads[headKey]
		if existing, ok := state.Records[recordKey]; ok {
			claim.Record = existing
			if existing.SnapshotHash != rec.SnapshotHash {
				claim.Status = intakeStatusConflict
				claim.Reason = "the work item snapshot changed without a revision change"
				return nil
			}
			// The same delivery retried after a crash between the durable write
			// and the activity acknowledgement must resume, not silently lose the
			// activation. A different delivery identity is a duplicate. A record
			// that already has an outcome is a duplicate in every case.
			if existing.Status == intakeRecordAccepted &&
				existing.Source == rec.Source &&
				existing.DeliveryID == rec.DeliveryID &&
				existing.ActivationID == rec.ActivationID {
				claim.Accepted = true
				claim.Status = intakeRecordAccepted
				claim.Record = existing
				return nil
			}
			claim.Status = intakeStatusDuplicate
			claim.Reason = "the work item revision was already recorded"
			return nil
		}
		if hasHead {
			switch compareWorkItemRevisions(rec.Revision, head.LatestRevision) {
			case -1:
				claim.Status = intakeStatusStale
				claim.Reason = "a newer work item revision was already processed"
				claim.Record = state.Records[intakeRecordKey(intakeRecord{
					Connector: rec.Connector, Repository: head.Repository, WorkItemID: head.WorkItemID, Revision: head.LatestRevision,
				})]
				return nil
			case 0:
				claim.Status = intakeStatusDuplicate
				claim.Reason = "the work item revision was already recorded"
				return nil
			}
			if rec.ParentRunID == "" {
				rec.ParentRunID = head.RunID
			}
		}
		state.Records[recordKey] = rec
		state.Heads[headKey] = intakeHead{
			Connector:      rec.Connector,
			Repository:     rec.Repository,
			WorkItemID:     rec.WorkItemID,
			LatestRevision: rec.Revision,
			SnapshotHash:   rec.SnapshotHash,
			ChangeID:       rec.ChangeID,
			RunID:          rec.RunID,
			ActivationID:   rec.ActivationID,
			UpdatedAt:      now,
		}
		claim.Accepted = true
		claim.Status = intakeRecordAccepted
		claim.Record = rec
		return nil
	})
	return claim, err
}

func (s *intakeStore) RecordOutcome(ctx context.Context, in WorkItemOutcomeInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.withState(func(state *intakeState) error {
		rec := intakeRecord{
			Connector: in.Connector, Repository: in.Repository,
			WorkItemID: in.WorkItemID, Revision: in.Revision,
		}
		recordKey := intakeRecordKey(rec)
		record, ok := state.Records[recordKey]
		if !ok {
			return fmt.Errorf("intake store: no record for %s revision %s", in.WorkItemID, in.Revision)
		}
		if in.Status == intakeStatusDuplicate && record.Status != intakeRecordAccepted {
			// A late duplicate-start report must not erase a completed outcome.
			return nil
		}
		record.Status = in.Status
		record.FactoryResult = in.FactoryResult
		record.Error = in.Error
		record.UpdatedAt = time.Now().UTC()
		state.Records[recordKey] = record
		headKey := intakeHeadKey(rec)
		if head, ok := state.Heads[headKey]; ok && head.LatestRevision == in.Revision {
			head.UpdatedAt = record.UpdatedAt
			state.Heads[headKey] = head
		}
		return nil
	})
}

func (s *intakeStore) Load() (intakeState, error) {
	state := intakeState{Heads: map[string]intakeHead{}, Records: map[string]intakeRecord{}}
	data, err := os.ReadFile(filepath.Join(s.root, intakeStateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("intake store: decode state: %w", err)
	}
	if state.Heads == nil {
		state.Heads = map[string]intakeHead{}
	}
	if state.Records == nil {
		state.Records = map[string]intakeRecord{}
	}
	return state, nil
}

func (s *intakeStore) withState(update func(*intakeState) error) error {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := root.OpenFile(intakeStateLockFileName, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	state, err := s.Load()
	if err != nil {
		return err
	}
	if err := update(&state); err != nil {
		return err
	}
	return s.save(state)
}

func (s *intakeStore) save(state intakeState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("intake store: encode state: %w", err)
	}
	tmp, err := os.CreateTemp(s.root, ".intake-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(s.root, intakeStateFileName)); err != nil {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validateIntakeRecord(rec intakeRecord) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"connector", rec.Connector},
		{"repository", rec.Repository},
		{"work item id", rec.WorkItemID},
		{"revision", rec.Revision},
		{"snapshot digest", rec.SnapshotHash},
		{"change id", rec.ChangeID},
		{"run id", rec.RunID},
		{"activation id", rec.ActivationID},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("intake store: %s is required", field.name)
		}
		if len(field.value) > maxIntakeFieldBytes || strings.ContainsAny(field.value, "\x00\r\n\t") {
			return fmt.Errorf("intake store: %s is invalid", field.name)
		}
	}
	return nil
}

func intakeRecordKey(rec intakeRecord) string {
	return digestText(strings.Join([]string{
		rec.Connector, rec.Repository, rec.WorkItemID, rec.Revision,
	}, "\x00"), 0)
}

func intakeHeadKey(rec intakeRecord) string {
	return digestText(strings.Join([]string{
		rec.Connector, rec.Repository, rec.WorkItemID,
	}, "\x00"), 0)
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

// appendIntakeAudit appends one durable intake decision.
//
// Intake rejections have no run of their own, so they cannot use a run's audit
// artifact. The intake audit is the record that a duplicate or stale delivery
// was seen and did not start work.
func appendIntakeAudit(store *intakeStore, entry AuditEntry, redactor *Redactor) error {
	entry = redactAuditEntry(entry, redactor)
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	if entry.Action == "" {
		entry.Action = AuditIntake
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	path := filepath.Join(store.root, intakeAuditFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func auditIntake(ctx context.Context, a *Activities, proposal forge.Proposal, resolution WorkItemResolution, outcome string) {
	if err := ctx.Err(); err != nil {
		return
	}
	store, err := openIntakeStore(a.artifactFactory.Root())
	if err != nil {
		return
	}
	runID := ""
	if resolution.Accepted {
		runID = resolution.RunID
	}
	err = appendIntakeAudit(store, AuditEntry{
		Action:          AuditIntake,
		RunID:           runID,
		Actor:           proposal.Actor.Descriptor,
		ActorKind:       ActorKindForge,
		ActorPermission: proposal.RepositoryPermission,
		Outcome:         outcome,
		Detail: map[string]any{
			"connector":             resolution.Connector,
			"pipeline":              resolution.Pipeline,
			"scope":                 resolution.Scope,
			"repository":            resolution.Repository,
			"work_item_id":          proposal.WorkItem.ID,
			"revision":              proposal.WorkItem.Revision,
			"snapshot_hash":         proposal.WorkItemHash,
			"actor_id":              proposal.Actor.ID,
			"actor_unique_name":     proposal.Actor.UniqueName,
			"repository_permission": proposal.RepositoryPermission,
			"status":                resolution.Status,
			"reason":                resolution.Reason,
		},
	}, a.redactor)
	if err != nil {
		a.log.WarnContext(ctx, "work item intake decision not audited",
			"connector", resolution.Connector, "work_item_id", proposal.WorkItem.ID,
			"revision", proposal.WorkItem.Revision, "status", resolution.Status, "error", err)
	}
}
