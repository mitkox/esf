package factory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
	"github.com/mitkox/esf/internal/verification"
	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/converter"
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
			Actor:        forge.Actor{ID: "aad-1", Descriptor: "aad.ada-lovelace", DisplayName: "Ada"},
		},
		DeliveryID:           "delivery-" + revision,
		ChangedFields:        []string{"System.State"},
		RepositoryPermission: forge.PermissionWrite,
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
	if manifest.Scope != "payments" {
		t.Fatalf("manifest scope = %q, want the resolved scope", manifest.Scope)
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
	if manifest.WorkItem.Project != "Payments" || manifest.WorkItem.State != "Active" {
		t.Fatalf("manifest work item identity = %#v, want the project and state", manifest.WorkItem)
	}
	if manifest.WorkItem.SnapshotHash != proposal.WorkItemHash {
		t.Fatalf("snapshot hash = %q, want %q", manifest.WorkItem.SnapshotHash, proposal.WorkItemHash)
	}
	if manifest.WorkItem.Actor == nil {
		t.Fatal("manifest did not bind the forge-resolved actor to the run")
	}
	if manifest.WorkItem.Actor.Descriptor != proposal.Actor.Descriptor ||
		manifest.WorkItem.Actor.RepositoryPermission != forge.PermissionWrite {
		t.Fatalf("manifest actor = %#v, want the resolved actor with write permission", manifest.WorkItem.Actor)
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

	var intakeState struct {
		Records map[string]struct {
			Connector  string `json:"connector"`
			WorkItemID string `json:"work_item_id"`
			Revision   string `json:"revision"`
			Scope      string `json:"scope"`
		} `json:"records"`
	}
	body, err := os.ReadFile(filepath.Join(harness.artifactDir, intakeDirName, intakeStateFileName))
	if err != nil {
		t.Fatalf("read durable intake state: %v", err)
	}
	if err := json.Unmarshal(body, &intakeState); err != nil {
		t.Fatalf("decode durable intake state: %v", err)
	}
	recorded := false
	for _, record := range intakeState.Records {
		if record.Connector != "azdo" || record.WorkItemID != "42" || record.Revision != "1" {
			continue
		}
		recorded = true
		if record.Scope != "payments" {
			t.Fatalf("intake claim scope = %q, want the resolved scope", record.Scope)
		}
	}
	if !recorded {
		t.Fatalf("the intake claim was not persisted: %s", body)
	}

	if len(harness.fake.Created()) != 1 {
		t.Fatalf("sandboxes created = %v, want one child", harness.fake.Created())
	}
	if live := harness.fake.Live(); len(live) != 0 {
		t.Fatalf("sandbox leaked: %v", live)
	}
}

// TestWorkItemChildOptionsUseRunWorkflowIdentity proves the pipeline child is
// started under the identity every run control addresses, with the same
// filterable memo a directly started run carries. An accepted resolution always
// has a run ID; a resolution without one keeps the activation identity rather
// than starting a child no control can address.
func TestWorkItemChildOptionsUseRunWorkflowIdentity(t *testing.T) {
	runID := WorkItemRunID("azdo", workItemTestRepository, "42", "1")
	activationID := ChangeActivationWorkflowID("azdo", workItemTestRepository, "42", "1")
	resolved := WorkItemResolution{
		RunID:        runID,
		ActivationID: activationID,
		Request: RunRequest{
			RunID:        runID,
			Repository:   workItemTestRepository,
			AgentHarness: "test-agent",
			Scope:        "payments",
		},
	}

	options := workItemChildOptions(resolved)
	if want := WorkflowIDForRun(runID); options.WorkflowID != want {
		t.Fatalf("child workflow ID = %q, want the run identity %q", options.WorkflowID, want)
	}
	if options.WorkflowID == activationID {
		t.Fatalf("child kept the activation identity %q", activationID)
	}
	if options.WorkflowIDReusePolicy != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
		t.Fatalf("reuse policy = %v, want REJECT_DUPLICATE", options.WorkflowIDReusePolicy)
	}
	memo, ok := options.Memo[RunMemoKey].(RunMemo)
	if !ok {
		t.Fatalf("child memo = %#v, want the run memo under %q", options.Memo, RunMemoKey)
	}
	wantMemo := RunMemo{Harness: "test-agent", Scope: "payments", Repository: workItemTestRepository}
	if memo != wantMemo {
		t.Fatalf("child memo = %+v, want %+v", memo, wantMemo)
	}

	withoutRun := resolved
	withoutRun.RunID = ""
	withoutRun.Request.RunID = ""
	if options := workItemChildOptions(withoutRun); options.WorkflowID != activationID {
		t.Fatalf("child without a run ID = %q, want the activation identity %q", options.WorkflowID, activationID)
	}
}

// TestWorkItemWorkflowChildIsDiscoverableAsRun proves a pipeline activation's
// child is started under the standard run workflow identity, so ListOpenRuns
// and the halt/cancel controls see it instead of discarding it as an unknown
// workflow.
func TestWorkItemWorkflowChildIsDiscoverableAsRun(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	var childWorkflowID string
	harness.env.SetOnChildWorkflowStartedListener(func(info *workflow.Info, _ workflow.Context, _ converter.EncodedValues) {
		childWorkflowID = info.WorkflowExecution.ID
	})

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Payment retry loop spins forever", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if len(outcome.Runs) != 1 {
		t.Fatalf("runs = %#v, want one child", outcome.Runs)
	}
	runID := outcome.Runs[0].RunID
	wantWorkflowID := WorkflowIDForRun(runID)
	if childWorkflowID != wantWorkflowID {
		t.Fatalf("child workflow ID = %q, want the run identity %q", childWorkflowID, wantWorkflowID)
	}

	// The console and halt path list executions by the standard identity and
	// filter on the run memo, so the started child must appear through the same
	// code path.
	execution := openRunInfo(t, runID, RunMemo{
		Harness: "test-agent", Scope: "payments", Repository: workItemTestRepository,
	})
	execution.Execution.WorkflowId = childWorkflowID
	c := &listClient{pages: [][]*workflowpb.WorkflowExecutionInfo{{execution}}}
	runs, err := ListOpenRuns(context.Background(), c, HaltFilter{Harness: "test-agent", Scope: "payments"})
	if err != nil {
		t.Fatalf("ListOpenRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != runID || runs[0].WorkflowID != wantWorkflowID {
		t.Fatalf("open runs = %+v, want the pipeline child as run %q", runs, runID)
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

// acceptanceSignalConnector posts one revision signal from inside the first
// acceptance read, the window where publication and the acceptance wait run
// after the child-workflow selector has returned.
type acceptanceSignalConnector struct {
	*forge.Fake
	once   sync.Once
	onRead func()
}

func (c *acceptanceSignalConnector) ReadAcceptance(ctx context.Context, req forge.AcceptanceRequest) (forge.AcceptanceResult, error) {
	c.once.Do(func() {
		if c.onRead != nil {
			c.onRead()
		}
	})
	return c.Fake.ReadAcceptance(ctx, req)
}

// TestWorkItemWorkflowSignalsRevisionDuringAcceptance proves a newer revision
// signalled while the acceptance read is pending still runs. The connector has
// already committed its sweep watermark for the accepted revision, so dropping
// the buffered signal would leave the newer edit unrun.
func TestWorkItemWorkflowSignalsRevisionDuringAcceptance(t *testing.T) {
	connector := &acceptanceSignalConnector{Fake: publicationFake()}
	signalled := false
	var harness *workItemTestEnvironment
	connector.onRead = func() {
		signalled = true
		harness.env.SignalWorkflow(WorkItemRevisionSignalName, WorkItemRevisionSignal{
			Proposal: workItemProposal("2", "Clarified bug", "Reproduction two"),
		})
	}
	harness = publicationHarness(t, connector)

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Original bug", "Reproduction one"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if !signalled {
		t.Fatal("the acceptance read never ran")
	}
	// One top-level execution reports both activations: the newer revision is
	// signalled to the running workflow rather than starting a second one.
	if len(outcome.Runs) != 2 {
		t.Fatalf("runs = %#v, want the accepted revision and the signalled revision", outcome.Runs)
	}
	if outcome.Runs[0].Revision != "1" || outcome.Runs[1].Revision != "2" {
		t.Fatalf("revision order = %q, %q", outcome.Runs[0].Revision, outcome.Runs[1].Revision)
	}
	if outcome.Runs[0].WorkflowID == outcome.Runs[1].WorkflowID {
		t.Fatalf("both runs used activation %q", outcome.Runs[0].WorkflowID)
	}
	secondManifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[1].RunID)
	if secondManifest.ParentRunID != outcome.Runs[0].RunID {
		t.Fatalf("second parent = %q, want %q", secondManifest.ParentRunID, outcome.Runs[0].RunID)
	}
	if len(harness.fake.Created()) != 2 {
		t.Fatalf("sandboxes created = %v, want one per revision", harness.fake.Created())
	}
	if live := harness.fake.Live(); len(live) != 0 {
		t.Fatalf("sandbox leaked: %v", live)
	}
}

// publicationSignalConnector posts one revision signal from inside the
// publication activity's authoritative work-item read.
type publicationSignalConnector struct {
	*forge.Fake
	once       sync.Once
	onReadItem func()
}

func (c *publicationSignalConnector) ReadWorkItem(ctx context.Context, id string) (forge.WorkItem, error) {
	c.once.Do(func() {
		if c.onReadItem != nil {
			c.onReadItem()
		}
	})
	return c.Fake.ReadWorkItem(ctx, id)
}

// TestWorkItemWorkflowSignalsRevisionDuringPublication proves a newer revision
// signalled while the publication activity is running is not dropped before
// the acceptance read starts.
func TestWorkItemWorkflowSignalsRevisionDuringPublication(t *testing.T) {
	connector := &publicationSignalConnector{Fake: publicationFake()}
	signalled := false
	var harness *workItemTestEnvironment
	connector.onReadItem = func() {
		signalled = true
		harness.env.SignalWorkflow(WorkItemRevisionSignalName, WorkItemRevisionSignal{
			Proposal: workItemProposal("2", "Clarified bug", "Reproduction two"),
		})
	}
	harness = publicationHarness(t, connector)

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Original bug", "Reproduction one"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if !signalled {
		t.Fatal("the publication activity never read the work item")
	}
	if len(outcome.Runs) != 2 {
		t.Fatalf("runs = %#v, want the published revision and the signalled revision", outcome.Runs)
	}
	if outcome.Runs[0].Revision != "1" || outcome.Runs[1].Revision != "2" {
		t.Fatalf("revision order = %q, %q", outcome.Runs[0].Revision, outcome.Runs[1].Revision)
	}
	// Each revision published its own branch and pull request under the one
	// top-level execution.
	if len(connector.Branches) != 2 || len(connector.Publications) != 2 {
		t.Fatalf("branches/publications = %d/%d, want one per revision",
			len(connector.Branches), len(connector.Publications))
	}
	if len(harness.fake.Created()) != 2 {
		t.Fatalf("sandboxes created = %v, want one per revision", harness.fake.Created())
	}
	if live := harness.fake.Live(); len(live) != 0 {
		t.Fatalf("sandbox leaked: %v", live)
	}
}

// TestWorkItemWorkflowSignalsWithdrawnRevision proves a newer revision that
// withdraws the work item still reaches the workflow: it is not dropped as a
// duplicate delivery, and publication stops before any forge write.
func TestWorkItemWorkflowSignalsWithdrawnRevision(t *testing.T) {
	connector := publicationFake()
	harness := publicationHarness(t, connector)
	withdrawn := workItemProposal("2", "Bug", "Reproduction")
	withdrawn.WorkItem.State = "Closed"
	harness.env.RegisterDelayedCallback(func() {
		harness.env.SignalWorkflow(WorkItemRevisionSignalName, WorkItemRevisionSignal{Proposal: withdrawn})
	}, time.Millisecond)

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Bug", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != workItemStatusWithdrawn {
		t.Fatalf("status = %q, want %s", outcome.Status, workItemStatusWithdrawn)
	}
	if len(outcome.Runs) != 2 {
		t.Fatalf("runs = %#v, want the original and the withdrawn revision", outcome.Runs)
	}
	if outcome.Runs[1].Revision != "2" {
		t.Fatalf("second revision = %q, want the signalled revision", outcome.Runs[1].Revision)
	}
	if len(connector.Branches) != 1 || len(connector.Publications) != 1 {
		t.Fatalf("branches/publications = %d/%d, want only the first revision",
			len(connector.Branches), len(connector.Publications))
	}
	publication := conditionByType(t, outcome.Conditions, ConditionPublication)
	if publication.Status != ConditionFalse || publication.Reason != "Withdrawn" {
		t.Fatalf("publication condition = %#v", publication)
	}
}

// TestWorkItemWorkflowIgnoresReplayedRevisionSignal proves a reconciliation
// replay of an already-delivered revision does not re-enter the workflow. The
// replay would re-resolve the durable intake record as a duplicate and replace
// a successful outcome with a duplicate decision.
func TestWorkItemWorkflowIgnoresReplayedRevisionSignal(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	proposal := workItemProposal("1", "Payment retry loop spins forever", "Reproduction")
	harness.env.RegisterDelayedCallback(func() {
		harness.env.SignalWorkflow(WorkItemRevisionSignalName, WorkItemRevisionSignal{Proposal: proposal})
	}, time.Millisecond)

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{Proposal: proposal})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != string(StateSucceeded) {
		t.Fatalf("status = %q, want SUCCEEDED (conditions: %#v)", outcome.Status, outcome.Conditions)
	}
	if len(outcome.Runs) != 1 {
		t.Fatalf("runs = %#v, want the one delivered revision", outcome.Runs)
	}
	intakeCondition := conditionByType(t, outcome.Conditions, ConditionIntake)
	if intakeCondition.Status != ConditionTrue || intakeCondition.Reason != "Accepted" {
		t.Fatalf("intake condition = %#v, want the accepted revision", intakeCondition)
	}
	if len(harness.fake.Created()) != 1 {
		t.Fatalf("sandboxes created = %v, want one", harness.fake.Created())
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

// TestWorkItemWorkflowSignalDuringPublicationVersionReplay proves a history
// recorded before workItemSignalDuringPublicationVersion replays on its
// original path. With the marker absent, publication and the acceptance wait
// run directly and a revision signalled during either stays buffered; the loop
// exits without a drain and the workflow records only the activation the
// history contains. Replaying into a selector would consume that signal and
// queue an activation the recorded history never had.
func TestWorkItemWorkflowSignalDuringPublicationVersionReplay(t *testing.T) {
	cases := []struct {
		name      string
		connector func(onSignal func()) forge.Connector
	}{
		{
			name: "publication",
			connector: func(onSignal func()) forge.Connector {
				c := &publicationSignalConnector{Fake: publicationFake()}
				c.onReadItem = onSignal
				return c
			},
		},
		{
			name: "acceptance",
			connector: func(onSignal func()) forge.Connector {
				c := &acceptanceSignalConnector{Fake: publicationFake()}
				c.onRead = onSignal
				return c
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			signalled := false
			var harness *workItemTestEnvironment
			connector := tc.connector(func() {
				signalled = true
				harness.env.SignalWorkflow(WorkItemRevisionSignalName, WorkItemRevisionSignal{
					Proposal: workItemProposal("2", "Clarified bug", "Reproduction two"),
				})
			})
			harness = publicationHarness(t, connector)
			// A history recorded before the marker has no version marker, so
			// the workflow reads the default and keeps its pre-marker path.
			harness.env.OnGetVersion(workItemSignalDuringPublicationVersion,
				workflow.DefaultVersion, workflow.Version(1)).
				Return(workflow.DefaultVersion).Once()

			outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
				Proposal: workItemProposal("1", "Original bug", "Reproduction one"),
			})
			if err != nil {
				t.Fatalf("work item workflow: %v", err)
			}
			if !signalled {
				t.Fatal("the publication or acceptance read never ran")
			}
			if len(outcome.Runs) != 1 || outcome.Runs[0].Revision != "1" {
				t.Fatalf("runs = %#v, want only the recorded revision", outcome.Runs)
			}
			if outcome.Status != string(StateSucceeded) {
				t.Fatalf("status = %q", outcome.Status)
			}
			if created := harness.fake.Created(); len(created) != 1 {
				t.Fatalf("sandboxes created = %v, want one", created)
			}
			if live := harness.fake.Live(); len(live) != 0 {
				t.Fatalf("sandbox leaked: %v", live)
			}
		})
	}
}

// TestWorkItemWorkflowVersionReplay covers the pipeline version marker: a
// history with the marker processes signals, while a history without it takes
// the single-revision compatibility path.
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

// TestWorkItemWorkflowWaitsForScopeSlot proves a work item at a full scope
// waits for admission instead of being dropped, and starts its sandbox only
// after the slot is released.
func TestWorkItemWorkflowWaitsForScopeSlot(t *testing.T) {
	artifactDir := t.TempDir()
	store, err := openScopeSlotStore(artifactDir)
	if err != nil {
		t.Fatalf("openScopeSlotStore: %v", err)
	}
	if claim, err := store.Acquire(context.Background(), "payments", "other-run", 1, time.Now().UTC()); err != nil || !claim.Granted {
		t.Fatalf("pre-acquire = %#v, %v", claim, err)
	}

	harness := newWorkItemTestEnvironment(t, artifactDir, withWorkItemConfig(func(cfg *Config) {
		scope := cfg.Scopes["payments"]
		scope.MaxConcurrentRuns = 1
		cfg.Scopes["payments"] = scope
	}))
	released := false
	harness.env.RegisterDelayedCallback(func() {
		_ = store.Release(context.Background(), "payments", "other-run")
		released = true
	}, 100*time.Millisecond)

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Payment retry loop spins forever", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if !released {
		t.Fatal("the work item workflow never waited for the scope slot")
	}
	if outcome.Status != string(StateSucceeded) {
		t.Fatalf("status = %q, want SUCCEEDED (conditions: %#v)", outcome.Status, outcome.Conditions)
	}
	if created := harness.fake.Created(); len(created) != 1 {
		t.Fatalf("sandboxes created = %v, want one after admission", created)
	}
	if live := harness.fake.Live(); len(live) != 0 {
		t.Fatalf("sandbox leaked: %v", live)
	}
}

// TestWorkItemWorkflowRejectsAmbiguousScope proves a work item that maps to
// two team scopes is rejected before a sandbox exists, even when one rule is
// more specific than the other.
func TestWorkItemWorkflowRejectsAmbiguousScope(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "", withWorkItemConfig(func(cfg *Config) {
		cfg.Scopes["identity"] = ScopeConfig{}
		cfg.Pipelines["bugfix"] = PipelineConfig{
			Connector: "azdo",
			Rules: []PipelineRule{
				{
					Project:             "Payments",
					Scope:               "payments",
					Repository:          workItemTestRepository,
					BaseBranch:          "main",
					VerificationProfile: "default",
				},
				{
					Project:             "Payments",
					Type:                "Bug",
					Scope:               "identity",
					Repository:          workItemTestRepository,
					BaseBranch:          "main",
					VerificationProfile: "default",
				},
			},
		}
	}))
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{
		Proposal: workItemProposal("1", "Payment retry loop spins forever", "Reproduction"),
	})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != intakeStatusScopeAmbiguous {
		t.Fatalf("status = %q, want scope_ambiguous", outcome.Status)
	}
	if len(harness.fake.Created()) != 0 {
		t.Fatalf("ambiguous scope created sandboxes: %v", harness.fake.Created())
	}
	condition := conditionByType(t, outcome.Conditions, ConditionIntake)
	if condition.Status != ConditionFalse || condition.Reason != "ScopeAmbiguous" {
		t.Fatalf("intake condition = %#v", condition)
	}
}

// TestWorkItemIntakeAuditBindsForgeActor proves the durable intake decision
// carries the forge-resolved actor, the repository permission, and the run it
// authorized. The actor is never taken from a payload display string.
func TestWorkItemIntakeAuditBindsForgeActor(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	proposal := workItemProposal("1", "Payment retry loop spins forever", "Reproduction")
	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{Proposal: proposal})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if len(outcome.Runs) != 1 {
		t.Fatalf("runs = %#v, want one child", outcome.Runs)
	}
	body, err := os.ReadFile(filepath.Join(harness.artifactDir, intakeDirName, intakeAuditFileName))
	if err != nil {
		t.Fatalf("read intake audit: %v", err)
	}
	var accepted *AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode audit entry: %v", err)
		}
		if entry.Action == AuditIntake && entry.Outcome == "SUCCESS" {
			accepted = &entry
		}
	}
	if accepted == nil {
		t.Fatalf("no accepted intake audit entry: %s", body)
	}
	if accepted.Actor != proposal.Actor.Descriptor {
		t.Fatalf("audit actor = %q, want %q", accepted.Actor, proposal.Actor.Descriptor)
	}
	if accepted.ActorKind != ActorKindForge || accepted.ActorPermission != forge.PermissionWrite {
		t.Fatalf("audit actor identity = %q/%q", accepted.ActorKind, accepted.ActorPermission)
	}
	if accepted.RunID != outcome.Runs[0].RunID {
		t.Fatalf("audit run = %q, want %q", accepted.RunID, outcome.Runs[0].RunID)
	}
	if accepted.Detail["scope"] != "payments" || accepted.Detail["repository_permission"] != forge.PermissionWrite {
		t.Fatalf("audit detail = %#v", accepted.Detail)
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

// TestSweepProposalResolvesAndStartsRunAfterJSONRoundTrip proves a
// reconciliation proposal keeps the forge-resolved actor through Temporal's
// JSON encoding. The sweep has no webhook payload, so the candidate's actor is
// the only source of the identity the intake gate checks.
func TestSweepProposalResolvesAndStartsRunAfterJSONRoundTrip(t *testing.T) {
	harness := newWorkItemTestEnvironment(t, "")
	base := workItemProposal("9", "Reconciled bug", "Recovered by sweep")
	candidate := forge.Candidate{
		Connector:    "azdo",
		Source:       forge.SourceSweep,
		WorkItem:     base.WorkItem,
		WorkItemHash: base.WorkItemHash,
		Actor:        base.Actor,
	}
	proposal := forge.Proposal{
		Candidate:            candidate,
		DeliveryID:           "sweep:" + candidate.WorkItem.Revision,
		RepositoryPermission: forge.PermissionWrite,
	}
	encoded, err := json.Marshal(proposal)
	if err != nil {
		t.Fatalf("marshal proposal: %v", err)
	}
	var decoded forge.Proposal
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal proposal: %v", err)
	}
	if decoded.Actor.Descriptor != base.Actor.Descriptor {
		t.Fatalf("actor descriptor after round trip = %q, want %q", decoded.Actor.Descriptor, base.Actor.Descriptor)
	}

	outcome, err := runWorkItemWorkflow(t, harness.env, WorkItemWorkflowInput{Proposal: decoded})
	if err != nil {
		t.Fatalf("work item workflow: %v", err)
	}
	if outcome.Status != string(StateSucceeded) {
		t.Fatalf("status = %q (conditions: %#v)", outcome.Status, outcome.Conditions)
	}
	if len(outcome.Runs) != 1 {
		t.Fatalf("runs = %#v, want one child started from the sweep", outcome.Runs)
	}
	manifest := readWorkItemRunManifest(t, harness.artifactDir, outcome.Runs[0].RunID)
	if manifest.WorkItem == nil || manifest.WorkItem.Source != string(forge.SourceSweep) {
		t.Fatalf("manifest work item = %#v, want the sweep source", manifest.WorkItem)
	}
	if manifest.WorkItem.Actor == nil || manifest.WorkItem.Actor.Descriptor != base.Actor.Descriptor {
		t.Fatalf("manifest actor = %#v, want the sweep-resolved actor", manifest.WorkItem.Actor)
	}
}
