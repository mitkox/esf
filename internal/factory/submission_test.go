package factory

import (
	"context"
	"reflect"
	"testing"
)

// The public execution API and the work-item pipeline are two entry points to
// one change-run submission contract. These tests drive the real constructor
// the API calls and the real request construction the pipeline activity uses,
// then compare what both produce.

func TestSubmissionContractIsSharedByAPIAndPipeline(t *testing.T) {
	env := newWorkItemTestEnvironment(t, "")
	proposal := workItemProposal("rev-1", "Fix the greeting", "The greeting shows the wrong user.")
	resolution, err := env.acts.ResolveWorkItemIntake(context.Background(), WorkItemResolveInput{Proposal: proposal})
	if err != nil {
		t.Fatalf("ResolveWorkItemIntake: %v", err)
	}
	if !resolution.Accepted {
		t.Fatalf("work item intake was not accepted: %s (%s)", resolution.Status, resolution.Reason)
	}

	// The API supplies only the shared fields. The pipeline-only fields stay
	// unset here; they are asserted as the documented delta below.
	apiRun, err := PrepareChangeRun(env.config, Submission{
		RunID:               WorkItemRunID("azdo", workItemTestRepository, "42", "rev-1"),
		Repository:          workItemTestRepository,
		Revision:            "main",
		Task:                workItemTask(proposal.WorkItem),
		AgentHarness:        "test-agent",
		VerificationProfile: "default",
		Scope:               "payments",
		ChangeID:            WorkItemChangeID("azdo", workItemTestRepository, "42"),
	})
	if err != nil {
		t.Fatalf("PrepareChangeRun: %v", err)
	}
	if apiRun.RepositoryKind != "remote" {
		t.Fatalf("repository kind = %q, want remote", apiRun.RepositoryKind)
	}

	pipelineRun := resolution.Request
	shared := pipelineRun
	shared.WorkItem = nil
	shared.ForgeConnector = ""
	shared.Review = nil
	if !reflect.DeepEqual(apiRun, shared) {
		t.Fatalf("the entry points disagree on shared request fields:\napi:      %+v\npipeline: %+v", apiRun, shared)
	}

	// The pipeline adds exactly the fields that describe its origin and its
	// review policy. The API never carries them.
	if pipelineRun.WorkItem == nil || pipelineRun.WorkItem.ID != "42" || pipelineRun.WorkItem.Connector != "azdo" {
		t.Fatalf("pipeline work item = %+v", pipelineRun.WorkItem)
	}
	if pipelineRun.ForgeConnector != "azdo" {
		t.Fatalf("pipeline forge connector = %q", pipelineRun.ForgeConnector)
	}
	if pipelineRun.Review == nil || *pipelineRun.Review {
		t.Fatalf("pipeline review override = %v, want an explicit false", pipelineRun.Review)
	}
	if apiRun.WorkItem != nil || apiRun.ForgeConnector != "" || apiRun.Review != nil {
		t.Fatalf("API submission carried pipeline-only fields: %+v", apiRun)
	}
}

func TestSubmissionValidationIsIdenticalForBothEntryPoints(t *testing.T) {
	env := newWorkItemTestEnvironment(t, "")
	// Empty operator defaults make the missing harness and profile cases fail
	// instead of being filled, so every mutation below is an invalid request.
	cfg := env.config
	cfg.Harnesses = nil
	cfg.Verification = nil

	base := Submission{
		RunID:               "run-1",
		Repository:          workItemTestRepository,
		Revision:            "main",
		Task:                "fix the greeting",
		AgentHarness:        "test-agent",
		VerificationProfile: "default",
		Scope:               "payments",
	}
	cases := []struct {
		name   string
		mutate func(*Submission)
	}{
		{name: "missing run id", mutate: func(s *Submission) { s.RunID = "" }},
		{name: "missing repository", mutate: func(s *Submission) { s.Repository = "" }},
		{name: "missing revision", mutate: func(s *Submission) { s.Revision = "" }},
		{name: "missing task", mutate: func(s *Submission) { s.Task = "" }},
		{name: "missing harness", mutate: func(s *Submission) { s.AgentHarness = "" }},
		{name: "missing verification profile", mutate: func(s *Submission) { s.VerificationProfile = "" }},
		{name: "run id outside one path segment", mutate: func(s *Submission) { s.RunID = "a/b" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := base
			tc.mutate(&sub)
			_, apiErr := PrepareChangeRun(cfg, sub)
			_, pipelineErr := workItemRunRequest(cfg, sub, nil, "azdo", false)
			if apiErr == nil || pipelineErr == nil {
				t.Fatalf("invalid submission was accepted: api=%v pipeline=%v", apiErr, pipelineErr)
			}
			if apiErr.Error() != pipelineErr.Error() {
				t.Fatalf("validation diverged:\napi:      %v\npipeline: %v", apiErr, pipelineErr)
			}
		})
	}
}

func TestPrepareChangeRunResolvesDefaultsWithoutReplacingResolvedValues(t *testing.T) {
	cfg := Default()
	cfg.Cube.TemplateID = "tpl-operator"
	base := Submission{
		RunID:      "run-1",
		Repository: "https://example.com/acme/repo",
		Revision:   "main",
		Task:       "fix the greeting",
	}

	run, err := PrepareChangeRun(cfg, base)
	if err != nil {
		t.Fatalf("PrepareChangeRun: %v", err)
	}
	wantHarness := ""
	for name := range cfg.Harnesses {
		wantHarness = name
	}
	if run.AgentHarness != wantHarness || run.VerificationProfile != "default" {
		t.Fatalf("operator defaults = harness %q, profile %q", run.AgentHarness, run.VerificationProfile)
	}
	if run.SandboxTemplate != "tpl-operator" {
		t.Fatalf("sandbox template = %q, want the operator default", run.SandboxTemplate)
	}
	if run.AgentTimeout != cfg.Limits.AgentTimeout.Std() ||
		run.VerificationTimeout != cfg.Limits.VerificationTimeout.Std() ||
		run.TotalTimeout != cfg.Limits.TotalTimeout.Std() {
		t.Fatalf("timeouts = %v/%v/%v, want the operator limits", run.AgentTimeout, run.VerificationTimeout, run.TotalTimeout)
	}

	// Values a caller resolved from its own policy are already set when the
	// shared step runs, so the operator defaults must leave them alone.
	resolved := base
	resolved.AgentHarness = "scoped-agent"
	resolved.VerificationProfile = "scoped-profile"
	run, err = PrepareChangeRun(cfg, resolved)
	if err != nil {
		t.Fatalf("PrepareChangeRun with resolved values: %v", err)
	}
	if run.AgentHarness != "scoped-agent" || run.VerificationProfile != "scoped-profile" {
		t.Fatalf("resolved values were replaced: harness %q, profile %q", run.AgentHarness, run.VerificationProfile)
	}
}
