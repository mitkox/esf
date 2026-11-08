package factory

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/notify"
	"github.com/mitkox/esf/internal/sandbox"
)

func publicationFake() *forge.Fake {
	fake := forge.NewFake(forge.Capabilities{
		DraftPullRequests: true,
		MaxBodyBytes:      4000,
		StatusKind:        forge.StatusKindPullRequestStatus,
		Attachments:       true,
	})
	fake.BranchResult = forge.BranchPublicationResult{
		Branch:       "esf/change-a1",
		Commit:       "commit-1",
		Tree:         "tree-1",
		ChangedPaths: []string{"greeting.py"},
	}
	fake.PublishResult = forge.PublishResult{PullRequest: forge.PullRequest{
		ID: "17", URL: "https://dev.azure.com/acme/payments/_git/payments/pullrequest/17", Draft: true,
	}}
	fake.AcceptanceResult = forge.AcceptanceResult{
		PullRequest: forge.PullRequest{ID: "17"},
		State:       forge.AcceptanceMerged, MergeCommit: "merge-1", MergeTree: "tree-1",
		Votes:             []forge.ApprovalVote{{Reviewer: "ada", Vote: 10}},
		AutoCompleteSetBy: "gracy",
		ClosedBy:          "ada",
	}
	return fake
}

func publicationHarness(t *testing.T, connector *forge.Fake) *workItemTestEnvironment {
	t.Helper()
	keyPath, _ := writeSigningKey(t)
	return newWorkItemTestEnvironment(t, "",
		withWorkItemConfig(enableEvidenceSigning(keyPath)),
		withWorkItemConnector("azdo", connector),
	)
}

func TestWorkItemWorkflowPublishesDraftAndAcceptsMergedPullRequest(t *testing.T) {
	connector := publicationFake()
	harness := publicationHarness(t, connector)
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Payment retry loop spins forever", "Steps to reproduce...\n\nExpected: retry stops."),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != string(StateSucceeded) {
		t.Fatalf("status = %q (conditions: %#v)", outcome.Status, outcome.Conditions)
	}
	publication := conditionByType(t, outcome.Conditions, ConditionPublication)
	if publication.Status != ConditionTrue || publication.Reason != "Published" {
		t.Fatalf("publication condition = %#v", publication)
	}
	acceptance := conditionByType(t, outcome.Conditions, ConditionAcceptance)
	if acceptance.Status != ConditionTrue || acceptance.Reason != "Merged" {
		t.Fatalf("acceptance condition = %#v", acceptance)
	}
	if len(connector.Publications) != 1 {
		t.Fatalf("publications = %d, want 1", len(connector.Publications))
	}
	if len(harness.fake.Created()) != 1 {
		t.Fatalf("sandboxes = %v", harness.fake.Created())
	}
	spec, ok := harness.fake.Spec(harness.fake.Created()[0])
	if !ok {
		t.Fatal("the sandbox spec was not recorded")
	}
	for _, env := range spec.Env {
		if strings.Contains(env, harness.config.Evidence.SigningKeyFile) {
			t.Fatalf("the signing key path reached the sandbox environment: %q", env)
		}
		for _, connectorConfig := range harness.config.Connectors {
			if connectorConfig.TokenFile != "" && strings.Contains(env, connectorConfig.TokenFile) {
				t.Fatalf("a publishing credential path reached the sandbox environment: %q", env)
			}
		}
	}
	plan := connector.Publications[0]
	if !plan.Draft {
		t.Fatal("the pull request was not planned as a draft")
	}
	if plan.Attachment == nil || plan.Attachment.Name != forge.EvidenceBundleAttachmentName {
		t.Fatalf("attachment = %#v", plan.Attachment)
	}
	if !strings.Contains(plan.Body, "Evidence bundle digest: sha256:") {
		t.Fatalf("body dropped the bundle digest: %q", plan.Body)
	}
	if strings.Contains(plan.Body, "Steps to reproduce") || strings.Contains(plan.Title, "Payment retry loop") {
		t.Fatalf("agent or work-item prose reached the pull request: title=%q body=%q", plan.Title, plan.Body)
	}
	if len(connector.Reports) != 1 || connector.Reports[0].Status.Name != forge.VerificationStatusName ||
		connector.Reports[0].Status.State != forge.StatusSucceeded {
		t.Fatalf("status reports = %#v", connector.Reports)
	}
	if outcome.PullRequest == nil || outcome.PullRequest.ID != "17" || outcome.PullRequest.Tree != "tree-1" {
		t.Fatalf("outcome pull request = %#v", outcome.PullRequest)
	}
	if outcome.Acceptance == nil || outcome.Acceptance.State != forge.AcceptanceMerged || outcome.Acceptance.MergeCommit != "merge-1" {
		t.Fatalf("outcome acceptance = %#v", outcome.Acceptance)
	}

	manifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[0].RunID)
	if manifest.PullRequest == nil || manifest.PullRequest.Commit != "commit-1" || manifest.PullRequest.Tree != "tree-1" ||
		!strings.HasPrefix(manifest.PullRequest.BundleDigest, "sha256:") {
		t.Fatalf("manifest pull request = %#v", manifest.PullRequest)
	}
	if manifest.Acceptance == nil || manifest.Acceptance.MergeCommit != "merge-1" {
		t.Fatalf("manifest acceptance = %#v", manifest.Acceptance)
	}
	changes, err := NewChangeStore(harness.artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	change, err := changes.Load(outcome.Runs[0].ChangeID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Status != ChangeDone {
		t.Fatalf("change status = %s, want DONE", change.Status)
	}
	if change.PullRequest == nil || change.PullRequest.ID != "17" || change.Acceptance == nil {
		t.Fatalf("change publication = %#v / %#v", change.PullRequest, change.Acceptance)
	}

	// A retried activity addresses the same publication and must not open a
	// second pull request.
	again, err := harness.acts.PublishChange(context.Background(), PublishChangeInput{RunID: outcome.Runs[0].RunID})
	if err != nil {
		t.Fatalf("retried PublishChange: %v", err)
	}
	if !again.Published || again.PullRequestID != "17" || len(connector.Publications) != 1 {
		t.Fatalf("retried publication = %#v, publications=%d", again, len(connector.Publications))
	}
	repeated, err := harness.acts.ReadPublicationAcceptance(context.Background(), ReadPublicationAcceptanceInput{RunID: outcome.Runs[0].RunID})
	if err != nil {
		t.Fatalf("repeated acceptance read: %v", err)
	}
	if repeated.Decision != string(publicationDecisionAlreadyApplied) || repeated.ChangeStatus != ChangeDone {
		t.Fatalf("repeated acceptance = %#v", repeated)
	}
	// Losing the acceptance index is repairable from the authoritative
	// manifest, and the merge is applied again without marking a different
	// change done.
	if err := os.RemoveAll(filepath.Join(harness.artifactDir, publicationDirName)); err != nil {
		t.Fatal(err)
	}
	repaired, err := harness.acts.ReadPublicationAcceptance(context.Background(), ReadPublicationAcceptanceInput{RunID: outcome.Runs[0].RunID})
	if err != nil {
		t.Fatalf("acceptance repair: %v", err)
	}
	if repaired.Decision != string(publicationDecisionAccepted) || repaired.ChangeStatus != ChangeDone {
		t.Fatalf("repaired acceptance = %#v", repaired)
	}
}

func TestWorkItemWorkflowProtectedPathGateBlocksPublication(t *testing.T) {
	connector := publicationFake()
	connector.BranchResult.ChangedPaths = []string{".github/workflows/ci.yml"}
	harness := publicationHarness(t, connector)
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Bug", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != string(StateGateTampered) {
		t.Fatalf("status = %q, want GATE_TAMPERED", outcome.Status)
	}
	publication := conditionByType(t, outcome.Conditions, ConditionPublication)
	if publication.Status != ConditionFalse {
		t.Fatalf("publication condition = %#v", publication)
	}
	if len(connector.Publications) != 0 {
		t.Fatalf("a gated change opened a pull request: %#v", connector.Publications)
	}
	manifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[0].RunID)
	if manifest.Hardening == nil || len(manifest.Hardening.Alerts) == 0 {
		t.Fatal("the gate failure raised no durable alert")
	}
	if manifest.Hardening.Alerts[0].Kind != AlertPublicationTampered {
		t.Fatalf("alert kind = %q", manifest.Hardening.Alerts[0].Kind)
	}
	changes, err := NewChangeStore(harness.artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	change, err := changes.Load(outcome.Runs[0].ChangeID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Status == ChangeDone || change.Status == ChangeInReview {
		t.Fatalf("change status = %s after a gated publication", change.Status)
	}
	localFactory, err := artifacts.NewLocal(harness.artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	local, err := localFactory.ForRun(outcome.Runs[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := ReadAudit(local)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range audit {
		if entry.Action == AuditPublication && entry.Outcome == "FAILED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("publication failure was not audited: %#v", audit)
	}
}

func TestPublicationEvidenceMismatchBlocksAndAlerts(t *testing.T) {
	keyPath, _ := writeSigningKey(t)
	artifactDir := t.TempDir()
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{
		artifactDir:  artifactDir,
		mutateConfig: enableEvidenceSigning(keyPath),
	})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory result = %s", manifest.FactoryResult)
	}
	local, err := artifacts.NewLocal(artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := local.ForRun(manifest.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ArtifactEvidenceBundle, []byte(`{"payloadType":"agent-authored"}`)); err != nil {
		t.Fatal(err)
	}
	activities := &Activities{
		artifactFactory: local,
		cfg:             Config{Evidence: EvidenceConfig{SigningKeyFile: keyPath}},
		redactor:        NewRedactor(),
		log:             slog.New(slog.DiscardHandler),
	}
	out, err := activities.PublishChange(context.Background(), PublishChangeInput{RunID: manifest.RunID})
	if err != nil {
		t.Fatalf("PublishChange: %v", err)
	}
	if out.Published || out.FailureClass != "evidence_mismatch" {
		t.Fatalf("publication result = %#v", out)
	}
	revised := readWorkItemRunManifest(t, artifactDir, manifest.RunID)
	if revised.Hardening == nil || len(revised.Hardening.Alerts) == 0 || revised.Hardening.Alerts[0].Kind != AlertPublicationTampered {
		t.Fatalf("alerts = %#v", revised.Hardening)
	}
	changes, err := NewChangeStore(artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	changeID := manifest.ChangeID
	if changeID == "" {
		changeID = manifest.RunID
	}
	change, err := changes.Load(changeID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Status == ChangeDone || change.Status == ChangeInReview {
		t.Fatalf("change status = %s", change.Status)
	}
	audit, err := ReadAudit(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) == 0 || audit[len(audit)-1].Action != AuditPublication || audit[len(audit)-1].Outcome != "FAILED" {
		t.Fatalf("audit = %#v", audit)
	}
}

func TestWorkItemWorkflowEmptyPatchOpensNoPullRequest(t *testing.T) {
	connector := publicationFake()
	harness := publicationHarness(t, connector)
	harness.fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute(""))
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Bug", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != string(StateSucceeded) {
		t.Fatalf("status = %q", outcome.Status)
	}
	publication := conditionByType(t, outcome.Conditions, ConditionPublication)
	if publication.Reason != "EmptyPatch" {
		t.Fatalf("publication condition = %#v", publication)
	}
	if len(connector.Publications) != 0 || len(connector.Branches) != 0 {
		t.Fatalf("an empty patch published: %#v / %#v", connector.Branches, connector.Publications)
	}
	acceptance := conditionByType(t, outcome.Conditions, ConditionAcceptance)
	if acceptance.Reason != "NotApplicable" {
		t.Fatalf("acceptance condition = %#v", acceptance)
	}
}

func TestWorkItemWorkflowAcceptanceDriftDoesNotMarkDone(t *testing.T) {
	connector := publicationFake()
	connector.AcceptanceResult.MergeTree = "tree-other"
	harness := publicationHarness(t, connector)
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Bug", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != workItemStatusAcceptanceDrift {
		t.Fatalf("status = %q, want acceptance_drift", outcome.Status)
	}
	acceptance := conditionByType(t, outcome.Conditions, ConditionAcceptance)
	if acceptance.Status != ConditionFalse || acceptance.Reason != "Drift" {
		t.Fatalf("acceptance condition = %#v", acceptance)
	}
	changes, err := NewChangeStore(harness.artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	change, err := changes.Load(outcome.Runs[0].ChangeID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Status == ChangeDone {
		t.Fatal("a merged tree mismatch marked the change done")
	}
	manifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[0].RunID)
	if manifest.Acceptance == nil || !strings.Contains(manifest.Acceptance.Note, "does not match") {
		t.Fatalf("manifest acceptance = %#v", manifest.Acceptance)
	}
	if manifest.Hardening == nil || len(manifest.Hardening.Alerts) == 0 || manifest.Hardening.Alerts[0].Kind != AlertAcceptanceDrift {
		t.Fatalf("drift alert = %#v", manifest.Hardening)
	}
}

func TestWorkItemWorkflowClosedPullRequestReturnsToRework(t *testing.T) {
	cases := []struct {
		name       string
		closedBy   string
		wantStatus string
		wantReason string
		wantOpen   bool
	}{
		{name: "human close", closedBy: "Ada Lovelace", wantStatus: workItemStatusRework, wantReason: "Closed", wantOpen: true},
		{name: "service close", closedBy: "Project Collection Build Service (acme)", wantStatus: workItemStatusAcceptanceUnknown, wantReason: "ServiceClosed"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			connector := publicationFake()
			connector.AcceptanceResult = forge.AcceptanceResult{
				PullRequest: forge.PullRequest{ID: "17"},
				State:       forge.AcceptanceClosed,
				ClosedBy:    tc.closedBy,
			}
			harness := publicationHarness(t, connector)
			outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
				Proposal: workItemProposal("1", "Bug", "Reproduction"),
			})
			if err != nil {
				t.Fatalf("work item workflow: %v", err)
			}
			if outcome.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", outcome.Status, tc.wantStatus)
			}
			acceptance := conditionByType(t, outcome.Conditions, ConditionAcceptance)
			if acceptance.Status != ConditionFalse || acceptance.Reason != tc.wantReason {
				t.Fatalf("acceptance condition = %#v", acceptance)
			}
			changes, err := NewChangeStore(harness.artifactDir)
			if err != nil {
				t.Fatal(err)
			}
			change, err := changes.Load(outcome.Runs[0].ChangeID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantOpen && change.Status != ChangeOpen {
				t.Fatalf("change status = %s, want OPEN", change.Status)
			}
			if !tc.wantOpen && change.Status == ChangeDone {
				t.Fatal("a service-identity close marked the change done")
			}
		})
	}
}

func TestWorkItemWorkflowForgeFailureIsVisible(t *testing.T) {
	connector := publicationFake()
	connector.PublishErr = errors.New("forge unavailable")
	harness := publicationHarness(t, connector)
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Bug", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != workItemStatusPublicationFailed {
		t.Fatalf("status = %q", outcome.Status)
	}
	manifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[0].RunID)
	if manifest.Hardening == nil || len(manifest.Hardening.Alerts) == 0 || manifest.Hardening.Alerts[0].Kind != AlertPublicationFailed {
		t.Fatalf("manifest alerts = %#v", manifest.Hardening)
	}
	changes, err := NewChangeStore(harness.artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	change, err := changes.Load(outcome.Runs[0].ChangeID)
	if err != nil {
		t.Fatal(err)
	}
	if change.Status == ChangeDone || change.Status == ChangeInReview {
		t.Fatalf("change status = %s after a publication failure", change.Status)
	}
}

func TestPublicationFailureAlertNamesClassAndChange(t *testing.T) {
	var mu sync.Mutex
	var received []notify.Event
	webhook := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			t.Errorf("decode alert: %v", err)
		}
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		response.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	connector := publicationFake()
	connector.PublishErr = errors.New("forge unavailable")
	keyPath, _ := writeSigningKey(t)
	harness := newWorkItemTestEnvironment(t, "",
		withWorkItemConfig(func(cfg *Config) {
			enableEvidenceSigning(keyPath)(cfg)
			cfg.Hardening.AlertWebhookURL = webhook.URL
		}),
		withWorkItemConnector("azdo", connector),
	)
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Bug", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != workItemStatusPublicationFailed || len(outcome.Runs) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("alerts delivered = %d, want 1", len(received))
	}
	event := received[0]
	if event.Kind != AlertPublicationFailed {
		t.Fatalf("alert kind = %q", event.Kind)
	}
	if event.Detail["class"] != "forge_failed" {
		t.Fatalf("alert class = %q (detail %+v)", event.Detail["class"], event.Detail)
	}
	if event.Detail["change"] != outcome.Runs[0].ChangeID {
		t.Fatalf("alert change = %q, want %q", event.Detail["change"], outcome.Runs[0].ChangeID)
	}
	if event.RunID != outcome.Runs[0].RunID {
		t.Fatalf("alert run = %q, want %q", event.RunID, outcome.Runs[0].RunID)
	}
}
