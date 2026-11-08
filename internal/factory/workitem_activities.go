package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/repository"
)

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
		return rejectWorkItemIntake(ctx, a, proposal, WorkItemResolution{Connector: connector}, intakeMappingFailureStatus(err), err.Error())
	}

	resolved, err := a.resolvePipeline(connector, in.Pipeline, fields)
	if err != nil {
		return rejectWorkItemIntake(ctx, a, proposal, WorkItemResolution{Connector: connector}, intakeMappingFailureStatus(err), err.Error())
	}
	if resolved.Scope != scope {
		return rejectWorkItemIntake(ctx, a, proposal, WorkItemResolution{Connector: connector}, intakeStatusRejected,
			fmt.Sprintf("scope resolution disagreed: %q resolved from the work item, %q from the pipeline",
				scope, resolved.Scope))
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
		return rejectWorkItemIntake(ctx, a, proposal, resolution, intakeStatusRejected, "the work item snapshot is empty")
	}
	harness, err := a.defaultHarness(scope)
	if err != nil {
		return rejectWorkItemIntake(ctx, a, proposal, resolution, intakeStatusRejected, err.Error())
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
		return rejectWorkItemIntake(ctx, a, proposal, resolution, intakeStatusRejected, err.Error())
	}

	resources, err := a.cfg.ResolveResources(request)
	if err != nil {
		return rejectWorkItemIntake(ctx, a, proposal, resolution, intakeStatusRejected, err.Error())
	}
	if err := a.repos.Validate(repository.Request{
		Kind: repository.SourceRemote, URL: resolved.Repository, Revision: resolved.BaseBranch,
	}); err != nil {
		return rejectWorkItemIntake(ctx, a, proposal, resolution, intakeStatusRejected, err.Error())
	}
	if !matchesAnyPrefix(resolved.Repository, resources.RepositoriesAllowed) {
		return rejectWorkItemIntake(ctx, a, proposal, resolution, intakeStatusRejected,
			fmt.Sprintf("repository %q is not allowed in scope %q", resolved.Repository, resources.Scope))
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
		Scope:               resolution.Scope,
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

// rejectWorkItemIntake records the rejection status and reason, audits the
// decision, and returns the resolution to the workflow.
func rejectWorkItemIntake(ctx context.Context, a *Activities, proposal forge.Proposal, resolution WorkItemResolution, status, reason string) (WorkItemResolution, error) {
	resolution.Status = status
	resolution.Reason = reason
	auditIntake(ctx, a, proposal, resolution, AuditOutcomeRejected)
	return resolution, nil
}

// intakeMappingFailureStatus names the mapping failure: an ambiguous match is
// distinguishable from a missing one so the workflow condition states the cause.
func intakeMappingFailureStatus(err error) string {
	var ambiguous *AmbiguousScopeError
	if errors.As(err, &ambiguous) {
		return intakeStatusScopeAmbiguous
	}
	return intakeStatusNoMapping
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
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut])
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
