package factory

import (
	"context"
	"fmt"
	"strings"
	"time"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/forge/azuredevops"
	"github.com/mitkox/esf/internal/notify"
)

const publicationAcceptancePollInterval = 5 * time.Second

// PublishChangeInput identifies the completed run to publish.
type PublishChangeInput struct {
	RunID string `json:"run_id"`
}

// PublishChangeOutput is the durable outcome of one publication attempt.
type PublishChangeOutput struct {
	Disabled   bool `json:"disabled,omitempty"`
	EmptyPatch bool `json:"empty_patch,omitempty"`
	Published  bool `json:"published"`
	GateFailed bool `json:"gate_failed,omitempty"`
	// Withdrawn reports that the authoritative work item was already closed
	// before any remote write, so no branch or pull request was created.
	Withdrawn    bool   `json:"withdrawn,omitempty"`
	FailureClass string `json:"failure_class,omitempty"`
	Failure      string `json:"failure,omitempty"`

	Branch         string   `json:"branch,omitempty"`
	Commit         string   `json:"commit,omitempty"`
	Tree           string   `json:"tree,omitempty"`
	BundleDigest   string   `json:"bundle_digest,omitempty"`
	PullRequestID  string   `json:"pull_request_id,omitempty"`
	PullRequestURL string   `json:"pull_request_url,omitempty"`
	StatusName     string   `json:"status_name,omitempty"`
	ChangedPaths   []string `json:"changed_paths,omitempty"`
}

// ReadPublicationAcceptanceInput identifies the run whose pull request should
// be observed. Wait polls while the pull request is open; zero performs one
// read.
type ReadPublicationAcceptanceInput struct {
	RunID string        `json:"run_id"`
	Wait  time.Duration `json:"wait,omitempty"`
}

// ReadPublicationAcceptanceOutput records what the forge reported.
type ReadPublicationAcceptanceOutput struct {
	Read         bool              `json:"read"`
	Decision     string            `json:"decision,omitempty"`
	Acceptance   *AcceptanceRecord `json:"acceptance,omitempty"`
	ChangeStatus ChangeStatus      `json:"change_status,omitempty"`
	Drift        bool              `json:"drift,omitempty"`
	// TimedOut reports that the bounded acceptance wait ended with the pull
	// request still open.
	TimedOut       bool   `json:"timed_out,omitempty"`
	Note           string `json:"note,omitempty"`
	PullRequestID  string `json:"pull_request_id,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
	MergeCommit    string `json:"merge_commit,omitempty"`
}

// PublishChange builds the evidenced patch into a deterministic host-side
// commit, gates the resulting tree, pushes the branch, and opens or updates the
// draft pull request. It never merges or approves anything.
func (a *Activities) PublishChange(ctx context.Context, in PublishChangeInput) (PublishChangeOutput, error) {
	if strings.TrimSpace(in.RunID) == "" {
		return PublishChangeOutput{}, fmt.Errorf("publication: a run id is required")
	}
	manifest, err := ReadManifest(a.artifactFactory, in.RunID)
	if err != nil {
		return PublishChangeOutput{}, err
	}
	if manifest.FactoryResult != StateSucceeded {
		return PublishChangeOutput{}, fmt.Errorf("publication: run %s did not succeed (%s)", manifest.RunID, manifest.FactoryResult)
	}
	if manifest.PullRequest != nil {
		// Already published; the store and forge calls are idempotent, but a
		// replay must not build a second branch attempt.
		return PublishChangeOutput{
			Published:      true,
			Branch:         manifest.PullRequest.Branch,
			Commit:         manifest.PullRequest.Commit,
			Tree:           manifest.PullRequest.Tree,
			PullRequestID:  manifest.PullRequest.ID,
			PullRequestURL: manifest.PullRequest.URL,
			StatusName:     forge.VerificationStatusName,
		}, nil
	}
	if !a.cfg.Evidence.configured() {
		return PublishChangeOutput{Disabled: true}, nil
	}
	if manifest.VerificationMutatedTree {
		return a.failPublication(ctx, manifest, "verification_drift", "a verification gate changed the tree after the evidenced patch was captured", AlertPublicationFailed, notify.SeverityWarning), nil
	}
	if strings.TrimSpace(manifest.Patch) == "" {
		return PublishChangeOutput{}, fmt.Errorf("publication: run %s has no patch artifact", manifest.RunID)
	}
	store, err := a.artifactFactory.ForRun(manifest.RunID)
	if err != nil {
		return PublishChangeOutput{}, err
	}
	patch, err := store.Read(manifest.Patch)
	if err != nil {
		return a.failPublication(ctx, manifest, "patch_unavailable", "the evidenced patch could not be read", AlertPublicationFailed, notify.SeverityWarning), nil
	}
	if strings.TrimSpace(string(patch)) == "" {
		return PublishChangeOutput{EmptyPatch: true}, nil
	}
	bundle, err := store.Read(ArtifactEvidenceBundle)
	if err != nil {
		return a.failPublication(ctx, manifest, "evidence_missing", "the signed evidence bundle is missing", AlertPublicationTampered, notify.SeverityCritical), nil
	}
	bundleDigest, gateCount, err := a.verifyEvidenceAgainstManifest(store, manifest)
	if err != nil {
		return a.failPublication(ctx, manifest, "evidence_mismatch", err.Error(), AlertPublicationTampered, notify.SeverityCritical), nil
	}

	connectorName := ""
	if manifest.WorkItem != nil {
		connectorName = strings.TrimSpace(manifest.WorkItem.Connector)
	}
	if connectorName == "" {
		return a.failPublication(ctx, manifest, "connector_unavailable", "the run has no forge connector", AlertPublicationFailed, notify.SeverityWarning), nil
	}
	connector, err := a.forgeConnector(connectorName)
	if err != nil {
		return a.failPublication(ctx, manifest, "connector_unavailable", err.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
	}
	publisher, ok := connector.(forge.BranchBuilder)
	if !ok {
		return a.failPublication(ctx, manifest, "connector_unavailable", "the forge connector cannot build and gate a branch before pushing", AlertPublicationFailed, notify.SeverityWarning), nil
	}
	caps := connector.Capabilities()
	if err := caps.Validate(); err != nil {
		return a.failPublication(ctx, manifest, "connector_capabilities", err.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
	}
	if manifest.WorkItem != nil {
		state := strings.TrimSpace(manifest.WorkItem.State)
		if reader, ok := connector.(forge.WorkItemStateReader); ok {
			current, readErr := reader.ReadWorkItem(ctx, manifest.WorkItem.ID)
			if readErr != nil {
				return a.failPublication(ctx, manifest, "work_item_unavailable", readErr.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
			}
			if live := strings.TrimSpace(current.State); live != "" {
				state = live
			}
		}
		if workItemWithdrawn(state) {
			return a.withdrawPublication(ctx, manifest, state), nil
		}
	}

	changeID := strings.TrimSpace(manifest.ChangeID)
	if changeID == "" {
		changeID = manifest.RunID
	}
	attempt := 1
	var changes *ChangeStore
	if changes, err = NewChangeStore(a.artifactFactory.Root()); err == nil {
		if change, loadErr := changes.Load(changeID); loadErr == nil && len(change.Activations) > 0 {
			attempt = len(change.Activations)
		}
	}
	branch := azuredevops.PublicationBranchName(changeID, attempt)
	commitTime := manifest.CompletedAt
	if commitTime.IsZero() {
		commitTime = manifest.StartedAt
	}
	if commitTime.IsZero() {
		return a.failPublication(ctx, manifest, "commit_time", "the manifest carries no completion timestamp", AlertPublicationFailed, notify.SeverityWarning), nil
	}
	commitMessage := fmt.Sprintf("ESF verified change %s (attempt %d)\n\nRun: %s\nEvidence bundle digest: %s", changeID, attempt, manifest.RunID, bundleDigest)
	title := publicationTitle(manifest, changeID)

	if err := a.appendPublicationAudit(store, manifest, AuditOutcomeAttempted, "", map[string]any{
		"branch":        branch,
		"bundle_digest": bundleDigest,
		"attempt":       attempt,
	}); err != nil {
		a.log.WarnContext(ctx, "publication attempt not audited", "run.id", manifest.RunID, "error", err)
	}

	branchRequest := forge.BranchPublication{
		Repository:  manifest.Repository,
		RemoteURL:   manifest.Repository,
		BaselineSHA: manifest.BaselineSHA,
		Branch:      branch,
		ChangeID:    changeID,
		Attempt:     attempt,
		Message:     commitMessage,
		AuthorName:  "ESF Factory",
		AuthorEmail: "esf-factory+" + digestText(manifest.RunID, 16) + "@localhost",
		CommitTime:  commitTime,
		Patch:       patch,
	}
	// Build the commit locally first. Every publication gate must run before
	// the commit can reach the forge: a protected path must never be pushed
	// even once, because a push-triggered pipeline can execute it.
	built, err := publisher.BuildBranch(ctx, branchRequest)
	if err != nil {
		return a.failPublication(ctx, manifest, "branch_failed", err.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
	}
	defer publisher.DiscardBranch(built)
	if strings.TrimSpace(built.Commit) == "" || strings.TrimSpace(built.Tree) == "" {
		return a.failPublication(ctx, manifest, "branch_incomplete", "the forge branch result did not identify a commit and tree", AlertPublicationFailed, notify.SeverityWarning), nil
	}
	gate, err := EvaluatePublicationGates(PublicationGateInput{
		ChangedPaths:          built.ChangedPaths,
		ProtectedPaths:        a.cfg.PublicationProtectedPaths(),
		BaselineGitmodules:    built.GitmodulesBaseline,
		ResultGitmodules:      built.GitmodulesResult,
		SubmoduleURLAllowlist: a.cfg.Hardening.SubmoduleURLAllowlist,
	})
	if err != nil {
		return a.failPublication(ctx, manifest, "gate_error", err.Error(), AlertPublicationTampered, notify.SeverityCritical), nil
	}
	if !gate.Passed {
		class := make([]string, 0, len(gate.Classes))
		for _, value := range gate.Classes {
			class = append(class, string(value))
		}
		out := a.failPublication(ctx, manifest, "protected_path",
			"publication gate rejected protected paths: "+strings.Join(class, ", "),
			AlertPublicationTampered, notify.SeverityCritical)
		out.GateFailed = true
		out.FailureClass = strings.Join(class, ",")
		out.ChangedPaths = append([]string(nil), built.ChangedPaths...)
		return out, nil
	}
	branchResult, err := publisher.PushBranch(ctx, branchRequest, built)
	if err != nil {
		return a.failPublication(ctx, manifest, "branch_failed", err.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
	}
	if strings.TrimSpace(branchResult.Commit) == "" || strings.TrimSpace(branchResult.Tree) == "" {
		return a.failPublication(ctx, manifest, "branch_incomplete", "the forge branch result did not identify a commit and tree", AlertPublicationFailed, notify.SeverityWarning), nil
	}

	summary := fmt.Sprintf("Repository: <%s>\n\nFactory-verified patch rebuilt on baseline %s. %d deterministic gate(s) passed. Branch: %s.",
		manifest.Repository, manifest.BaselineSHA, gateCount, branch)
	plan, err := forge.PlanPublication(caps, forge.Publication{
		WorkItem:     workItemRefForManifest(manifest),
		Repository:   manifest.Repository,
		BaseBranch:   manifest.RequestedRevision,
		Branch:       branch,
		Title:        title,
		Summary:      summary,
		BundleDigest: bundleDigest,
		Bundle:       bundle,
		Draft:        true,
	})
	if err != nil {
		return a.failPublication(ctx, manifest, "plan_failed", err.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
	}
	published, err := connector.Publish(ctx, plan)
	if err != nil {
		return a.failPublication(ctx, manifest, "forge_failed", err.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
	}
	if strings.TrimSpace(published.PullRequest.ID) == "" {
		return a.failPublication(ctx, manifest, "forge_incomplete", "the forge did not identify the pull request", AlertPublicationFailed, notify.SeverityWarning), nil
	}
	statusErr := connector.Report(ctx, forge.ReportRequest{
		WorkItem:      plan.WorkItem,
		Repository:    manifest.Repository,
		PullRequestID: published.PullRequest.ID,
		// The work-item comment is the developer-facing outcome; the status is
		// the branch-policy signal. They are posted independently.
		Comment: "ESF verification passed. Pull request: " + published.PullRequest.URL +
			" Evidence bundle: " + bundleDigest,
		Status: forge.Status{
			Name:        forge.VerificationStatusName,
			State:       forge.StatusSucceeded,
			Description: "Signed evidence verified: " + bundleDigest,
			TargetURL:   published.PullRequest.URL,
		},
	})
	if statusErr != nil {
		// The pull request exists and is the published result. A status post is
		// a reporting concern: losing it must not move an otherwise published
		// change back to OPEN or invite a duplicate publication on retry.
		a.log.WarnContext(ctx, "publication report not fully delivered",
			"run.id", manifest.RunID, "pull_request", published.PullRequest.ID, "error", statusErr)
	}

	record, err := a.recordPublication(manifest, published.PullRequest, branchResult, branch, bundleDigest)
	if err != nil {
		return a.failPublication(ctx, manifest, "record_failed", err.Error(), AlertPublicationFailed, notify.SeverityWarning), nil
	}
	if changes != nil {
		identity := PullRequestIdentity{
			ID: record.PullRequestID, URL: record.PullRequestURL, Repository: record.Repository,
			Branch: record.Branch, Draft: published.PullRequest.Draft,
			Commit: record.Commit, Tree: record.Tree, BundleDigest: bundleDigest,
		}
		if _, err := changes.RecordPullRequest(changeID, identity, time.Now().UTC()); err != nil {
			a.log.WarnContext(ctx, "published pull request not recorded on the change", "change.id", changeID, "error", err)
		}
	}
	manifest.PullRequest = &PullRequestIdentity{
		ID: published.PullRequest.ID, URL: published.PullRequest.URL, Repository: manifest.Repository,
		Branch: branch, Draft: published.PullRequest.Draft,
		Commit: branchResult.Commit, Tree: branchResult.Tree, BundleDigest: bundleDigest,
	}
	manifest.Conditions = SetCondition(manifest.Conditions, Condition{
		Type: ConditionPublication, Status: ConditionTrue, Reason: "Published",
		Message:            "pull request " + published.PullRequest.ID + " branch " + branch,
		LastTransitionTime: time.Now().UTC(),
	})
	if err := a.writeJSON(store, ArtifactRun, manifest); err != nil {
		return PublishChangeOutput{}, err
	}
	if err := a.appendPublicationAudit(store, manifest, "SUCCESS", "", map[string]any{
		"branch":        branch,
		"pull_request":  published.PullRequest.ID,
		"commit":        branchResult.Commit,
		"tree":          branchResult.Tree,
		"bundle_digest": bundleDigest,
		"changed_paths": len(branchResult.ChangedPaths),
	}); err != nil {
		a.log.WarnContext(ctx, "publication success not audited", "run.id", manifest.RunID, "error", err)
	}
	return PublishChangeOutput{
		Published:      true,
		Branch:         branch,
		Commit:         branchResult.Commit,
		Tree:           branchResult.Tree,
		BundleDigest:   bundleDigest,
		PullRequestID:  published.PullRequest.ID,
		PullRequestURL: published.PullRequest.URL,
		StatusName:     forge.VerificationStatusName,
		ChangedPaths:   append([]string(nil), branchResult.ChangedPaths...),
	}, nil
}

// ReadPublicationAcceptance reads the forge merge state. A merged pull request
// marks the change done when its merge-source commit matches the commit the
// publication record already stores; matching trees remain the fallback for an
// observation without a source commit.
func (a *Activities) ReadPublicationAcceptance(ctx context.Context, in ReadPublicationAcceptanceInput) (ReadPublicationAcceptanceOutput, error) {
	if strings.TrimSpace(in.RunID) == "" {
		return ReadPublicationAcceptanceOutput{}, fmt.Errorf("acceptance: a run id is required")
	}
	manifest, err := ReadManifest(a.artifactFactory, in.RunID)
	if err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	changeID := strings.TrimSpace(manifest.ChangeID)
	if changeID == "" {
		changeID = manifest.RunID
	}
	publications, err := openPublicationStore(a.artifactFactory.Root())
	if err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	record, hasRecord, err := publications.Load(changeID)
	if err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	if !hasRecord && manifest.PullRequest != nil &&
		strings.TrimSpace(manifest.PullRequest.Commit) != "" &&
		strings.TrimSpace(manifest.PullRequest.Tree) != "" {
		// Repair a publication record lost after the manifest was written. The
		// manifest is authoritative; the record is an acceptance index.
		connector := ""
		if manifest.WorkItem != nil {
			connector = manifest.WorkItem.Connector
		}
		record, err = publications.RecordPublication(publicationRecord{
			ChangeID: changeID, RunID: manifest.RunID, Connector: connector,
			Repository: manifest.Repository, BaseBranch: manifest.RequestedRevision,
			Branch: manifest.PullRequest.Branch, PullRequestID: manifest.PullRequest.ID,
			PullRequestURL: manifest.PullRequest.URL, Commit: manifest.PullRequest.Commit,
			Tree: manifest.PullRequest.Tree, BundleDigest: manifest.PullRequest.BundleDigest,
		})
		if err != nil {
			return ReadPublicationAcceptanceOutput{}, err
		}
		hasRecord = true
	}
	pullRequestID := ""
	pullRequestURL := ""
	connectorName := ""
	repository := manifest.Repository
	if manifest.PullRequest != nil {
		pullRequestID = manifest.PullRequest.ID
		pullRequestURL = manifest.PullRequest.URL
		repository = firstNonEmptyString(manifest.PullRequest.Repository, repository)
	} else if hasRecord {
		pullRequestID = record.PullRequestID
		pullRequestURL = record.PullRequestURL
		repository = firstNonEmptyString(record.Repository, repository)
	}
	if manifest.WorkItem != nil {
		connectorName = strings.TrimSpace(manifest.WorkItem.Connector)
	}
	if connectorName == "" && hasRecord {
		connectorName = record.Connector
	}
	if pullRequestID == "" || connectorName == "" {
		return ReadPublicationAcceptanceOutput{Read: false}, nil
	}
	connector, err := a.forgeConnector(connectorName)
	if err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	deadline := time.Now().Add(in.Wait)
	var result forge.AcceptanceResult
	for {
		result, err = connector.ReadAcceptance(ctx, forge.AcceptanceRequest{
			Repository: repository, PullRequestID: pullRequestID,
		})
		if err != nil {
			return ReadPublicationAcceptanceOutput{}, err
		}
		if result.State != forge.AcceptanceOpen || in.Wait <= 0 || !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(publicationAcceptancePollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ReadPublicationAcceptanceOutput{}, ctx.Err()
		case <-timer.C:
		}
	}

	humanClosed := true
	if result.State == forge.AcceptanceClosed {
		humanClosed = humanClose(result.ClosedBy)
	}
	outcome, err := publications.RecordAcceptance(ctx, publicationAcceptanceEvent{
		Connector: connectorName, Repository: repository, ChangeID: changeID, RunID: manifest.RunID,
		Result: result, HumanClosed: humanClosed,
	})
	if err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	out := ReadPublicationAcceptanceOutput{
		Read: true, Decision: string(outcome.Decision), PullRequestID: pullRequestID,
		PullRequestURL: pullRequestURL, MergeCommit: result.MergeCommit,
	}
	if outcome.Record.Acceptance != nil {
		acceptance := *outcome.Record.Acceptance
		out.Acceptance = &acceptance
	}
	var changes *ChangeStore
	changes, _ = NewChangeStore(a.artifactFactory.Root())
	switch outcome.Decision {
	case publicationDecisionAccepted:
		if changes != nil && out.Acceptance != nil {
			if change, err := changes.RecordAcceptance(changeID, *out.Acceptance, time.Now().UTC()); err == nil {
				out.ChangeStatus = change.Status
			}
		}
		manifest.Conditions = SetCondition(manifest.Conditions, Condition{
			Type: ConditionAcceptance, Status: ConditionTrue, Reason: "Merged",
			Message: "merge commit " + result.MergeCommit, LastTransitionTime: time.Now().UTC(),
		})
	case publicationDecisionAlreadyApplied:
		if changes != nil {
			if change, err := changes.Load(changeID); err == nil {
				out.ChangeStatus = change.Status
			}
		}
	case publicationDecisionClosedHuman:
		if changes != nil && out.Acceptance != nil {
			note := "closed without merging"
			if result.ClosedBy != "" {
				note = "closed by " + result.ClosedBy
			}
			out.Acceptance.Note = note
			if change, err := changes.RecordAcceptance(changeID, *out.Acceptance, time.Now().UTC()); err == nil {
				out.ChangeStatus = change.Status
			}
		}
		manifest.Conditions = SetCondition(manifest.Conditions, Condition{
			Type: ConditionAcceptance, Status: ConditionFalse, Reason: "Closed",
			Message: "the pull request was closed without merging", LastTransitionTime: time.Now().UTC(),
		})
	case publicationDecisionClosedService:
		note := "closed by an actor that could not be resolved as a human; human acceptance is unknown"
		out.Note = note
		if out.Acceptance != nil {
			out.Acceptance.Note = note
		}
		manifest = a.appendManifestAlert(ctx, manifest, a.raiseAlert(ctx, manifest.RunID, AlertAcceptanceUnresolved, notify.SeverityWarning,
			"pull request closed by an unresolved or service identity", map[string]string{
				"pull_request": pullRequestID, "closed_by": result.ClosedBy,
			}))
		manifest.Conditions = SetCondition(manifest.Conditions, Condition{
			Type: ConditionAcceptance, Status: ConditionFalse, Reason: "ServiceClosed",
			Message: note, LastTransitionTime: time.Now().UTC(),
		})
	case publicationDecisionDrift:
		out.Drift = true
		out.Note = outcome.Note
		manifest = a.appendManifestAlert(ctx, manifest, a.raiseAlert(ctx, manifest.RunID, AlertAcceptanceDrift, notify.SeverityWarning,
			"merged pull request does not match the published change", map[string]string{
				"pull_request": pullRequestID, "merge_commit": result.MergeCommit,
				"merge_tree": result.MergeTree, "source_commit": result.SourceCommit,
			}))
		manifest.Conditions = SetCondition(manifest.Conditions, Condition{
			Type: ConditionAcceptance, Status: ConditionFalse, Reason: "Drift",
			Message: outcome.Note, LastTransitionTime: time.Now().UTC(),
		})
	case publicationDecisionIgnoredStale:
		out.Note = "ignored a merge for a superseded pull request"
		out.Acceptance = nil
		manifest.Conditions = SetCondition(manifest.Conditions, Condition{
			Type: ConditionAcceptance, Status: ConditionUnknown, Reason: "Superseded",
			Message: out.Note, LastTransitionTime: time.Now().UTC(),
		})
	case publicationDecisionPending:
		out.Note = outcome.Note
		manifest.Conditions = SetCondition(manifest.Conditions, Condition{
			Type: ConditionAcceptance, Status: ConditionUnknown, Reason: "PendingPublication",
			Message: outcome.Note, LastTransitionTime: time.Now().UTC(),
		})
	default:
		manifest.Conditions = SetCondition(manifest.Conditions, Condition{
			Type: ConditionAcceptance, Status: ConditionUnknown, Reason: "Open",
			Message: "the pull request is still open", LastTransitionTime: time.Now().UTC(),
		})
	}
	if out.Acceptance != nil {
		manifest.Acceptance = out.Acceptance
	}
	runStore, err := a.artifactFactory.ForRun(manifest.RunID)
	if err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	if err := a.writeJSON(runStore, ArtifactRun, manifest); err != nil {
		return ReadPublicationAcceptanceOutput{}, err
	}
	if out.Decision != string(publicationDecisionAlreadyApplied) {
		if err := a.appendAcceptanceAudit(runStore, manifest, out); err != nil {
			a.log.WarnContext(ctx, "acceptance not audited", "run.id", manifest.RunID, "error", err)
		}
	}
	return out, nil
}

// withdrawPublication records a withdrawn work item without touching the
// forge. It is distinct from a publication failure: nothing was attempted on
// the remote because the authoritative work item no longer wants the change.
func (a *Activities) withdrawPublication(ctx context.Context, manifest RunManifest, state string) PublishChangeOutput {
	state = strings.TrimSpace(state)
	message := fmt.Sprintf("the work item is %s; no branch or pull request was opened", state)
	changeID := strings.TrimSpace(manifest.ChangeID)
	if changeID == "" {
		changeID = manifest.RunID
	}
	if changes, err := NewChangeStore(a.artifactFactory.Root()); err == nil {
		if err := changes.Abandon(changeID, time.Now().UTC()); err != nil {
			a.log.WarnContext(ctx, "withdrawn work item not recorded on the change", "change.id", changeID, "error", err)
		}
	}
	manifest.Conditions = SetCondition(manifest.Conditions, Condition{
		Type: ConditionPublication, Status: ConditionFalse, Reason: "Withdrawn",
		Message: message, LastTransitionTime: time.Now().UTC(),
	})
	manifest.Conditions = SetCondition(manifest.Conditions, Condition{
		Type: ConditionAcceptance, Status: ConditionUnknown, Reason: "Withdrawn",
		Message: "the work item was withdrawn before publication", LastTransitionTime: time.Now().UTC(),
	})
	if store, err := a.artifactFactory.ForRun(manifest.RunID); err == nil {
		if err := a.writeJSON(store, ArtifactRun, manifest); err != nil {
			a.log.WarnContext(ctx, "withdrawal not recorded in the manifest", "run.id", manifest.RunID, "error", err)
		}
		if err := a.appendPublicationAudit(store, manifest, "WITHDRAWN", message, map[string]any{"state": state}); err != nil {
			a.log.WarnContext(ctx, "withdrawal not audited", "run.id", manifest.RunID, "error", err)
		}
	}
	return PublishChangeOutput{
		Withdrawn:    true,
		FailureClass: "work_item_withdrawn",
		Failure:      message,
	}
}

// workItemWithdrawn reports whether a forge state means the work item no
// longer wants the change. The set is deliberately conservative: ambiguous
// states such as Resolved still publish.
func workItemWithdrawn(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "closed", "done", "removed", "completed", "cancelled", "canceled", "abandoned":
		return true
	default:
		return false
	}
}

func (a *Activities) failPublication(
	ctx context.Context,
	manifest RunManifest,
	class string,
	message string,
	alertKind string,
	severity notify.Severity,
) PublishChangeOutput {
	message = a.redactor.Redact(strings.TrimSpace(message))
	changeID := strings.TrimSpace(manifest.ChangeID)
	if changeID == "" {
		changeID = manifest.RunID
	}
	if metrics := a.telemetry.Metrics(); metrics != nil {
		metrics.RecordPublicationFailure(ctx, class)
		if strings.HasPrefix(class, "evidence_") {
			metrics.RecordEvidenceFailure(ctx, class)
		}
	}
	if changes, err := NewChangeStore(a.artifactFactory.Root()); err == nil {
		if _, err := changes.RecordPullRequestFailure(changeID, time.Now().UTC()); err != nil {
			a.log.WarnContext(ctx, "publication failure not recorded on the change", "change.id", changeID, "error", err)
		}
	}
	manifest = a.appendManifestAlert(ctx, manifest, a.raiseAlert(ctx, manifest.RunID, alertKind, severity,
		"publication failed: "+class, map[string]string{
			"class": class, "change": changeID, "error": message,
		}))
	manifest.Conditions = SetCondition(manifest.Conditions, Condition{
		Type: ConditionPublication, Status: ConditionFalse, Reason: class,
		Message: message, LastTransitionTime: time.Now().UTC(),
	})
	if store, err := a.artifactFactory.ForRun(manifest.RunID); err == nil {
		if err := a.writeJSON(store, ArtifactRun, manifest); err != nil {
			a.log.WarnContext(ctx, "publication failure not recorded in the manifest", "run.id", manifest.RunID, "error", err)
		}
		if err := a.appendPublicationAudit(store, manifest, "FAILED", message, map[string]any{"class": class}); err != nil {
			a.log.WarnContext(ctx, "publication failure not audited", "run.id", manifest.RunID, "error", err)
		}
	}
	return PublishChangeOutput{FailureClass: class, Failure: message}
}

func (a *Activities) recordPublication(
	manifest RunManifest,
	pullRequest forge.PullRequest,
	branchResult forge.BranchPublicationResult,
	branch, bundleDigest string,
) (publicationRecord, error) {
	changeID := strings.TrimSpace(manifest.ChangeID)
	if changeID == "" {
		changeID = manifest.RunID
	}
	store, err := openPublicationStore(a.artifactFactory.Root())
	if err != nil {
		return publicationRecord{}, err
	}
	connector := ""
	if manifest.WorkItem != nil {
		connector = manifest.WorkItem.Connector
	}
	record, err := store.RecordPublication(publicationRecord{
		ChangeID: changeID, RunID: manifest.RunID, Connector: connector,
		Repository: manifest.Repository, BaseBranch: manifest.RequestedRevision,
		Branch: branch, PullRequestID: pullRequest.ID, PullRequestURL: pullRequest.URL,
		Commit: branchResult.Commit, Tree: branchResult.Tree, BundleDigest: bundleDigest,
	})
	if err != nil {
		return publicationRecord{}, err
	}
	return record, nil
}

func (a *Activities) appendManifestAlert(ctx context.Context, manifest RunManifest, record AlertRecord) RunManifest {
	if manifest.Hardening == nil {
		manifest.Hardening = &HardeningEvidence{}
	}
	manifest.Hardening.Alerts = append(manifest.Hardening.Alerts, record)
	return manifest
}

func (a *Activities) appendPublicationAudit(store artifacts.Store, manifest RunManifest, outcome, message string, detail map[string]any) error {
	actor, permission := manifestActor(manifest)
	if detail == nil {
		detail = map[string]any{}
	}
	if permission != "" {
		detail["repository_permission"] = permission
	}
	return AppendAudit(store, AuditEntry{
		RunID: manifest.RunID, Action: AuditPublication, Outcome: outcome,
		Actor: actor, ActorKind: actorKind(actor), ActorPermission: permission,
		Detail: detail, Error: a.redactor.Redact(message),
	}, a.redactor)
}

func (a *Activities) appendAcceptanceAudit(store artifacts.Store, manifest RunManifest, out ReadPublicationAcceptanceOutput) error {
	actor, permission := manifestActor(manifest)
	detail := map[string]any{
		"pull_request": out.PullRequestID,
		"decision":     out.Decision,
		"merge_commit": out.MergeCommit,
		"drift":        out.Drift,
	}
	if permission != "" {
		detail["repository_permission"] = permission
	}
	return AppendAudit(store, AuditEntry{
		RunID: manifest.RunID, Action: AuditAcceptance, Outcome: strings.ToUpper(out.Decision),
		Actor: actor, ActorKind: actorKind(actor), ActorPermission: permission,
		Detail: detail,
	}, a.redactor)
}

// actorKind names the identity source without treating an empty actor as a
// forge identity. An empty actor keeps AppendAudit's operator attribution.
func actorKind(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return ""
	}
	return ActorKindForge
}

func workItemRefForManifest(manifest RunManifest) forge.WorkItemRef {
	if manifest.WorkItem == nil {
		return forge.WorkItemRef{}
	}
	return forge.WorkItemRef{
		ID: manifest.WorkItem.ID, Revision: manifest.WorkItem.Revision,
		Project: manifest.WorkItem.Project,
	}
}

func publicationTitle(manifest RunManifest, changeID string) string {
	title := "ESF: verified change " + changeID
	if manifest.WorkItem != nil && strings.TrimSpace(manifest.WorkItem.ID) != "" {
		title = "ESF: verified work item " + manifest.WorkItem.ID + " revision " + manifest.WorkItem.Revision
	}
	if len(title) > 200 {
		title = title[:200]
	}
	return strings.TrimSpace(title)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func humanClose(actor string) bool {
	actor = strings.TrimSpace(strings.ToLower(actor))
	if actor == "" {
		return false
	}
	if strings.TrimSpace(actor) == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for _, marker := range []string{
		"build service", "service account", "serviceaccount", "project collection",
		"azure devops", "vstfs:///", "system:",
	} {
		if strings.Contains(actor, marker) {
			return false
		}
	}
	return true
}
