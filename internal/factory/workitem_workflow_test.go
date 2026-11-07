package factory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
	"github.com/mitkox/esf/internal/verification"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
)

const (
	workItemTestRepository = "https://dev.azure.com/acme/payments/_git/payments"
	workItemTestToken      = "esf:pat"
)

type workItemTestEnvironment struct {
	env         *testsuite.TestWorkflowEnvironment
	acts        *Activities
	config      Config
	fake        *sandbox.Fake
	connector   forge.Connector
	artifactDir string
}

type workItemTestOptions struct {
	mutateConfig func(*Config)
	connectors   map[string]forge.Connector
}

type workItemTestOption func(*workItemTestOptions)

func withWorkItemConfig(mutate func(*Config)) workItemTestOption {
	return func(opts *workItemTestOptions) { opts.mutateConfig = mutate }
}

func withWorkItemConnector(name string, connector forge.Connector) workItemTestOption {
	return func(opts *workItemTestOptions) {
		if opts.connectors == nil {
			opts.connectors = map[string]forge.Connector{}
		}
		opts.connectors[name] = connector
	}
}

// newWorkItemTestEnvironment builds the same fake sandbox and real activity
// set the existing workflow harness uses, with one operator pipeline mapped to
// an Azure DevOps repository.
func newWorkItemTestEnvironment(t *testing.T, artifactDir string, options ...workItemTestOption) *workItemTestEnvironment {
	t.Helper()

	harness, err := agentharness.NewGeneric(agentharness.Spec{
		Name:       "test-agent",
		Executable: "agent",
		Args:       []string{"run", agentharness.ModelArgsPlaceholder},
		ModelFlag:  "--model",
		Timeout:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	registry := agentharness.NewRegistry()
	_ = registry.Register(harness)

	tokenFile := filepath.Join(t.TempDir(), "azdo.token")
	if err := os.WriteFile(tokenFile, []byte(workItemTestToken), 0o600); err != nil {
		t.Fatalf("write connector credential: %v", err)
	}

	mandatory := true
	cfg := Config{
		FactoryVersion: "test",
		Cube:           cubeConfigForTest(),
		Sandbox:        SandboxConfig{BasePackages: []string{"git"}},
		Repositories:   RepositoriesConfig{Allowed: []string{workItemTestRepository}},
		Verification: map[string]verification.Profile{
			"default": {
				Name:  "default",
				Steps: []verification.Step{{ID: "build", Argv: []string{"./build.sh"}, Mandatory: &mandatory}},
			},
		},
		Limits: LimitsConfig{
			AgentTimeout:        tomlx.FromStd(time.Minute),
			VerificationTimeout: tomlx.FromStd(time.Minute),
			TotalTimeout:        tomlx.FromStd(time.Hour),
		},
		Hardening: HardeningConfig{AcknowledgeOpenEgress: true},
		Scopes:    map[string]ScopeConfig{"payments": {}},
		Connectors: map[string]ConnectorConfig{
			"azdo": {Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme", TokenFile: tokenFile},
		},
		Pipelines: map[string]PipelineConfig{
			"bugfix": {
				Connector: "azdo",
				Rules: []PipelineRule{{
					Project:             "Payments",
					Type:                "Bug",
					Scope:               "payments",
					Repository:          workItemTestRepository,
					BaseBranch:          "main",
					VerificationProfile: "default",
				}},
			},
		},
	}
	opts := workItemTestOptions{}
	for _, option := range options {
		option(&opts)
	}
	if opts.mutateConfig != nil {
		opts.mutateConfig(&cfg)
	}
	if artifactDir == "" {
		artifactDir = t.TempDir()
	}
	artifactFactory, err := artifacts.NewLocal(artifactDir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	telemetry, err := NewTelemetry(context.Background(), ObservabilityConfig{})
	if err != nil {
		t.Fatalf("NewTelemetry: %v", err)
	}
	acts, err := NewActivities(ActivitiesOptions{
		Provider:        sandbox.NewFake(),
		Repositories:    repository.New(cfg.Repositories.Allowed),
		Harnesses:       registry,
		Profiles:        cfg.VerificationProfiles(),
		ArtifactFactory: artifactFactory,
		Redactor:        NewRedactor(workItemTestToken),
		Telemetry:       telemetry,
		Config:          cfg,
		Connectors:      opts.connectors,
		Logger:          slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewActivities: %v", err)
	}
	fake := sandbox.NewFake()
	// The activity set holds the provider; the environment keeps the same fake
	// so the test can inspect the sandbox creation spec.
	acts.provider = fake
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(WorkItemWorkflow)
	env.RegisterWorkflow(SoftwareChangeWorkflow)
	env.RegisterActivity(acts)
	return &workItemTestEnvironment{
		env: env, acts: acts, config: cfg, fake: fake, connector: opts.connectors["azdo"], artifactDir: artifactDir,
	}
}

func workItemProposal(revision, title, body string) forge.Proposal {
	workItem := forge.WorkItem{
		ID:       "42",
		Revision: revision,
		WorkItemFields: forge.WorkItemFields{
			Project: "Payments",
			Type:    "Bug",
			State:   "Active",
		},
		Title: title,
		Body:  body,
	}
	return forge.Proposal{
		Candidate: forge.Candidate{
			Connector:    "azdo",
			Source:       forge.SourceWebhook,
			WorkItem:     workItem,
			WorkItemHash: digestText(title+"\x00"+body, 0),
		},
		Actor:         forge.Actor{ID: "aad-1", Descriptor: "aad.ada-lovelace", DisplayName: "Ada"},
		DeliveryID:    "delivery-" + revision,
		ChangedFields: []string{"System.State"},
	}
}

func runWorkItemWorkflow(t *testing.T, env *testsuite.TestWorkflowEnvironment, input WorkItemWorkflowInput) (WorkItemOutcome, error) {
	t.Helper()
	env.ExecuteWorkflow(WorkItemWorkflow, input)
	if !env.IsWorkflowCompleted() {
		t.Fatal("work item workflow did not complete")
	}
	var outcome WorkItemOutcome
	err := env.GetWorkflowResult(&outcome)
	return outcome, err
}

func readWorkItemRunManifest(t *testing.T, artifactDir, runID string) RunManifest {
	t.Helper()
	local, err := artifacts.NewLocal(artifactDir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	store, err := local.ForRun(runID)
	if err != nil {
		t.Fatalf("ForRun(%s): %v", runID, err)
	}
	body, err := store.Read(ArtifactRun)
	if err != nil {
		t.Fatalf("read manifest for %s: %v", runID, err)
	}
	var manifest RunManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
}

func conditionByType(t *testing.T, conditions []Condition, conditionType ConditionType) Condition {
	t.Helper()
	condition, ok := ConditionFor(conditions, conditionType)
	if !ok {
		t.Fatalf("condition %s is missing: %#v", conditionType, conditions)
	}
	return condition
}

// TestWorkItemWorkflowStartsOneChildWithMappedRepository proves the core
// path: an intake event is resolved by an activity, one software change child
// runs against the operator-mapped repository, and the exact work-item bytes are
// hashed into the task.
func TestWorkItemWorkflowStartsOneChildWithMappedRepository(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	proposal := workItemProposal("1", "Payment retry loop spins forever", "Steps to reproduce...\n\nExpected: retry stops.")
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{Proposal: proposal})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != string(StateSucceeded) {
		t.Fatalf("status = %q, want SUCCEEDED (conditions: %#v)", outcome.Status, outcome.Conditions)
	}
	if len(outcome.Runs) != 1 {
		t.Fatalf("runs = %#v, want one child", outcome.Runs)
	}
	intakeCondition := conditionByType(t, outcome.Conditions, ConditionIntake)
	if intakeCondition.Status != ConditionTrue {
		t.Fatalf("intake condition = %#v", intakeCondition)
	}
	if publication := conditionByType(t, outcome.Conditions, ConditionPublication); publication.Status == ConditionTrue {
		t.Fatalf("publication condition must not claim a publication: %#v", publication)
	}
	if acceptance := conditionByType(t, outcome.Conditions, ConditionAcceptance); acceptance.Status == ConditionTrue {
		t.Fatalf("acceptance condition must not claim an acceptance: %#v", acceptance)
	}

	manifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[0].RunID)
	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("child result = %s", manifest.FactoryResult)
	}
	if manifest.Repository != workItemTestRepository {
		t.Fatalf("manifest repository = %q, want the operator mapping", manifest.Repository)
	}
	if manifest.RequestedRevision != "main" {
		t.Fatalf("requested revision = %q, want the mapped base branch", manifest.RequestedRevision)
	}
	if manifest.WorkItem == nil {
		t.Fatal("manifest did not record the work item origin")
	}
	if manifest.WorkItem.Connector != "azdo" || manifest.WorkItem.ID != "42" || manifest.WorkItem.Revision != "1" {
		t.Fatalf("manifest work item = %#v", manifest.WorkItem)
	}
	if manifest.WorkItem.SnapshotHash != proposal.WorkItemHash {
		t.Fatalf("snapshot hash = %q, want %q", manifest.WorkItem.SnapshotHash, proposal.WorkItemHash)
	}
	if manifest.TaskHash != repository.HashTask(workItemTask(proposal.WorkItem)) {
		t.Fatalf("task hash = %q, want the hash of the work-item task text", manifest.TaskHash)
	}
	if manifest.ChangeID != WorkItemChangeID("azdo", workItemTestRepository, "42") {
		t.Fatalf("change id = %q", manifest.ChangeID)
	}
	if manifest.ParentRunID != "" {
		t.Fatalf("first activation has parent %q", manifest.ParentRunID)
	}

	if len(harness.fake.Created()) != 1 {
		t.Fatalf("sandboxes created = %v, want one child", harness.fake.Created())
	}
	if live := harness.fake.Live(); len(live) != 0 {
		t.Fatalf("sandbox leaked: %v", live)
	}
}

// TestWorkItemWorkflowInjectsCloneCredentialAtEgress proves the mapped private
// repository gets its credential at the Cube boundary and not in the sandbox,
// task artifact, or manifest.
func TestWorkItemWorkflowInjectsCloneCredentialAtEgress(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	proposal := workItemProposal("1", "Payment retry loop spins forever", "Reproduction")
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{Proposal: proposal})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if len(harness.fake.Created()) != 1 {
		t.Fatalf("created = %v", harness.fake.Created())
	}
	spec, ok := harness.fake.Spec(harness.fake.Created()[0])
	if !ok {
		t.Fatal("fake provider did not record the create spec")
	}
	if len(spec.Network.Rules) != 2 {
		t.Fatalf("network rules = %#v, want the two clone endpoints", spec.Network.Rules)
	}
	wantSecret := base64.StdEncoding.EncodeToString([]byte(workItemTestToken))
	for _, rule := range spec.Network.Rules {
		if rule.Match.Host != "dev.azure.com" {
			t.Fatalf("clone rule host = %q", rule.Match.Host)
		}
		if len(rule.Action.Inject) != 1 || rule.Action.Inject[0].Secret != wantSecret {
			t.Fatalf("clone rule injection = %#v", rule.Action.Inject)
		}
		if !strings.HasPrefix(rule.Match.Path, "/acme/payments/_git/payments/") {
			t.Fatalf("clone rule path = %q", rule.Match.Path)
		}
	}
	for _, env := range spec.Env {
		if strings.Contains(env, workItemTestToken) {
			t.Fatal("the forge credential was placed in the sandbox environment")
		}
	}

	local, err := artifacts.NewLocal(harness.artifactDir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := local.ForRun(outcome.Runs[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{ArtifactTask, ArtifactRun} {
		body, err := store.Read(artifact)
		if err != nil {
			t.Fatalf("read %s: %v", artifact, err)
		}
		if strings.Contains(string(body), workItemTestToken) || strings.Contains(string(body), wantSecret) {
			t.Fatalf("%s leaked the clone credential", artifact)
		}
	}
}

// TestWorkItemWorkflowRejectsDuplicateRevision proves a second delivery for the
// same revision is recorded as a duplicate and starts no child.
func TestWorkItemWorkflowRejectsDuplicateRevision(t *testing.T) {
	artifactDir := t.TempDir()
	first := newWorkItemTestEnvironment(t, artifactDir)
	proposal := workItemProposal("1", "Payment retry loop spins forever", "Reproduction")
	if _, err := runWorkItemWorkflow(t, first.env, WorkItemWorkflowInput{Proposal: proposal}); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if len(first.fake.Created()) != 1 {
		t.Fatalf("first delivery created %v", first.fake.Created())
	}

	second := newWorkItemTestEnvironment(t, artifactDir)
	outcome, err := runWorkItemWorkflow(t, second.env, WorkItemWorkflowInput{Proposal: proposal})
	if err != nil {
		t.Fatalf("duplicate workflow: %v", err)
	}
	if outcome.Status != intakeStatusDuplicate {
		t.Fatalf("status = %q, want duplicate", outcome.Status)
	}
	if len(second.fake.Created()) != 0 {
		t.Fatalf("duplicate delivery created a sandbox: %v", second.fake.Created())
	}
	condition := conditionByType(t, outcome.Conditions, ConditionIntake)
	if condition.Status != ConditionFalse || condition.Reason != "Duplicate" {
		t.Fatalf("intake condition = %#v", condition)
	}
	audit, err := os.ReadFile(filepath.Join(artifactDir, intakeDirName, intakeAuditFileName))
	if err != nil {
		t.Fatalf("read intake audit: %v", err)
	}
	if !strings.Contains(string(audit), AuditOutcomeDuplicate) {
		t.Fatalf("duplicate decision was not audited: %s", audit)
	}
}

// TestWorkItemWorkflowSignalsRunningRevision proves a newer revision is a
// signal to the running workflow: both revisions run under one parent, one
// after the other, and both children clean up.
func TestWorkItemWorkflowSignalsRunningRevision(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	first := workItemProposal("1", "Original bug", "Reproduction one")
	second := workItemProposal("2", "Clarified bug", "Reproduction two")
	harness.env.RegisterDelayedCallback(func() {
		harness.env.SignalWorkflow(WorkItemRevisionSignalName, WorkItemRevisionSignal{Proposal: second})
	}, time.Millisecond)

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{Proposal: first})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if len(outcome.Runs) != 2 {
		t.Fatalf("runs = %#v, want two sequential activations", outcome.Runs)
	}
	if outcome.Runs[0].Revision != "1" || outcome.Runs[1].Revision != "2" {
		t.Fatalf("revision order = %q, %q", outcome.Runs[0].Revision, outcome.Runs[1].Revision)
	}
	if outcome.Status != string(StateSucceeded) {
		t.Fatalf("status = %q", outcome.Status)
	}
	if len(harness.fake.Created()) != 2 {
		t.Fatalf("sandboxes created = %v, want one per revision", harness.fake.Created())
	}
	if live := harness.fake.Live(); len(live) != 0 {
		t.Fatalf("sandbox leaked: %v", live)
	}
	secondManifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[1].RunID)
	if secondManifest.ParentRunID != outcome.Runs[0].RunID {
		t.Fatalf("second parent = %q, want %q", secondManifest.ParentRunID, outcome.Runs[0].RunID)
	}
}

// TestWorkItemWorkflowSkipsOlderRevision proves a late, older delivery does not
// run once a newer revision completed.
func TestWorkItemWorkflowSkipsOlderRevision(t *testing.T) {
	artifactDir := t.TempDir()
	newer := newWorkItemTestEnvironment(t, artifactDir)
	if _, err := runWorkItemWorkflow(t, newer.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("5", "Newer revision", "Newer body"),
	}); err != nil {
		t.Fatalf("newer revision: %v", err)
	}

	older := newWorkItemTestEnvironment(t, artifactDir)
	outcome, err := runWorkItemWorkflow(t, older.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("4", "Older revision", "Older body"),
	})
	if err != nil {
		t.Fatalf("older revision workflow: %v", err)
	}
	if outcome.Status != intakeStatusStale {
		t.Fatalf("status = %q, want stale", outcome.Status)
	}
	if len(older.fake.Created()) != 0 {
		t.Fatalf("older revision created a sandbox: %v", older.fake.Created())
	}
	condition := conditionByType(t, outcome.Conditions, ConditionIntake)
	if condition.Status != ConditionFalse || condition.Reason != "Stale" {
		t.Fatalf("intake condition = %#v", condition)
	}
}

// TestWorkItemWorkflowRejectsSnapshotChangeAtSameRevision proves a changed
// work-item snapshot is a distinct intake digest that cannot reuse the prior
// run when the revision did not move.
func TestWorkItemWorkflowRejectsSnapshotChangeAtSameRevision(t *testing.T) {
	artifactDir := t.TempDir()
	first := newWorkItemTestEnvironment(t, artifactDir)
	if _, err := runWorkItemWorkflow(t, first.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("7", "First snapshot", "Body one"),
	}); err != nil {
		t.Fatalf("first revision: %v", err)
	}

	changed := newWorkItemTestEnvironment(t, artifactDir)
	outcome, err := runWorkItemWorkflow(t, changed.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("7", "Changed snapshot", "Body two"),
	})
	if err != nil {
		t.Fatalf("changed snapshot workflow: %v", err)
	}
	if outcome.Status != intakeStatusConflict {
		t.Fatalf("status = %q, want snapshot_conflict", outcome.Status)
	}
	if len(changed.fake.Created()) != 0 {
		t.Fatalf("changed snapshot created a sandbox: %v", changed.fake.Created())
	}
	condition := conditionByType(t, outcome.Conditions, ConditionIntake)
	if condition.Status != ConditionFalse || condition.Reason != "SnapshotChanged" {
		t.Fatalf("intake condition = %#v", condition)
	}
}

// TestWorkItemWorkflowVerificationFailureStaysOpen proves a failed child keeps
// the change open and records no publication.
func TestWorkItemWorkflowVerificationFailureStaysOpen(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	base := wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	harness.fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		if strings.Contains(strings.Join(cmd.Argv, " "), "build.sh") {
			return sandbox.Execution{ExitCode: 1, Stderr: "build failed"}, nil
		}
		return base(cmd)
	}
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Payment retry loop spins forever", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if len(outcome.Runs) != 1 || outcome.Runs[0].FactoryResult != StateVerificationFailed {
		t.Fatalf("runs = %#v", outcome.Runs)
	}
	if outcome.Status != string(StateVerificationFailed) {
		t.Fatalf("status = %q", outcome.Status)
	}
	if publication := conditionByType(t, outcome.Conditions, ConditionPublication); publication.Status == ConditionTrue {
		t.Fatalf("a failed child must not record publication: %#v", publication)
	}
	if acceptance := conditionByType(t, outcome.Conditions, ConditionAcceptance); acceptance.Status == ConditionTrue {
		t.Fatalf("a failed child must not record acceptance: %#v", acceptance)
	}
	store, err := NewChangeStore(harness.artifactDir)
	if err != nil {
		t.Fatalf("NewChangeStore: %v", err)
	}
	change, err := store.Load(outcome.Runs[0].ChangeID)
	if err != nil {
		t.Fatalf("Load change: %v", err)
	}
	if change.Status != ChangeOpen {
		t.Fatalf("change status = %s, want OPEN", change.Status)
	}
	if len(harness.fake.Live()) != 0 {
		t.Fatalf("sandbox leaked: %v", harness.fake.Live())
	}
}

// TestWorkItemWorkflowVersionReplay covers the new version marker: a history
// with the marker processes signals, while a history without it takes the
// single-revision compatibility path.
func TestWorkItemWorkflowVersionReplay(t *testing.T) {
	cases := []struct {
		name    string
		version workflow.Version
	}{
		{name: "new marker", version: workflow.Version(1)},
		{name: "history without marker", version: workflow.DefaultVersion},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			harness := newWorkItemTestEnvironment(t, "")
			harness.env.OnGetVersion(workItemPipelineVersion, workflow.DefaultVersion, workflow.Version(1)).
				Return(tc.version).Once()
			outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
				Proposal: workItemProposal("1", "Payment retry loop spins forever", "Reproduction"),
			})
			if err != nil {
				t.Fatalf("work item workflow: %v", err)
			}
			if outcome.Status != string(StateSucceeded) {
				t.Fatalf("status = %q", outcome.Status)
			}
			if len(outcome.Runs) != 1 {
				t.Fatalf("runs = %#v", outcome.Runs)
			}
		})
	}
}

// TestIntakeStoreResumesSameDeliveryAndRejectsDifferentDelivery proves the
// durable record survives a restarted worker: the same delivery retried after a
// lost acknowledgement resumes, while a second delivery of the same revision is
// a duplicate.
func TestIntakeStoreResumesSameDeliveryAndRejectsDifferentDelivery(t *testing.T) {
	store, err := openIntakeStore(t.TempDir())
	if err != nil {
		t.Fatalf("openIntakeStore: %v", err)
	}
	record := intakeRecord{
		Connector:       "azdo",
		Pipeline:        "bugfix",
		Repository:      workItemTestRepository,
		WorkItemID:      "42",
		Revision:        "3",
		SnapshotHash:    "sha256:" + strings.Repeat("a", 64),
		Source:          string(forge.SourceWebhook),
		DeliveryID:      "delivery-1",
		ActorDescriptor: "aad.ada-lovelace",
		ChangeID:        "workitem-change",
		RunID:           "wi-run",
		ActivationID:    "factory-change-activation",
		BaseBranch:      "main",
	}
	first, err := store.Claim(context.Background(), record)
	if err != nil || !first.Accepted {
		t.Fatalf("first claim = %#v, %v", first, err)
	}
	retry, err := store.Claim(context.Background(), record)
	if err != nil || !retry.Accepted {
		t.Fatalf("retried claim = %#v, %v", retry, err)
	}
	duplicate := record
	duplicate.DeliveryID = "delivery-2"
	second, err := store.Claim(context.Background(), duplicate)
	if err != nil || second.Status != intakeStatusDuplicate {
		t.Fatalf("second delivery = %#v, %v", second, err)
	}
	changed := record
	changed.SnapshotHash = "sha256:" + strings.Repeat("b", 64)
	conflict, err := store.Claim(context.Background(), changed)
	if err != nil || conflict.Status != intakeStatusConflict {
		t.Fatalf("changed snapshot = %#v, %v", conflict, err)
	}
}
