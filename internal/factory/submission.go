package factory

import (
	"fmt"
	"sort"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
)

// Submission is the entry-point-neutral description of a change run.
//
// The public execution API maps its wire request onto it, and the first-party
// work-item pipeline fills it from the operator's forge mapping. It carries
// only the fields both entry points share, before either resolves its own
// defaults or adds its own fields. It describes a remote repository: the CLI's
// trusted host-directory fixture path is deliberately not part of this
// contract.
type Submission struct {
	RunID               string
	Repository          string
	Revision            string
	Task                string
	AgentHarness        string
	AgentModel          string
	VerificationProfile string
	Scope               string
	Workspace           string
	Model               string
	EgressPolicy        string
	ChangeID            string
	ParentRunID         string
}

// PrepareChangeRun normalizes and validates a submission into the input the
// change workflow consumes.
//
// A field the submitter left empty takes the operator default. A value the
// caller already resolved from its own policy — the pipeline resolves the
// harness and verification profile for the work item's scope — is never
// replaced. Because both entry points pass through this one step, an invalid
// submission is rejected the same way whichever surface made it.
func PrepareChangeRun(cfg Config, sub Submission) (RunRequest, error) {
	run := RunRequest{
		RunID:               sub.RunID,
		Repository:          sub.Repository,
		RepositoryKind:      string(repository.SourceRemote),
		Revision:            sub.Revision,
		Task:                sub.Task,
		SandboxTemplate:     cfg.Cube.TemplateID,
		AgentHarness:        sub.AgentHarness,
		AgentModel:          sub.AgentModel,
		VerificationProfile: sub.VerificationProfile,
		Scope:               sub.Scope,
		Workspace:           sub.Workspace,
		Model:               sub.Model,
		EgressPolicy:        sub.EgressPolicy,
		ChangeID:            sub.ChangeID,
		ParentRunID:         sub.ParentRunID,
		AgentTimeout:        cfg.Limits.AgentTimeout.Std(),
		VerificationTimeout: cfg.Limits.VerificationTimeout.Std(),
		TotalTimeout:        cfg.Limits.TotalTimeout.Std(),
	}
	if run.AgentHarness == "" {
		run.AgentHarness = firstConfiguredHarness(cfg)
	}
	if run.VerificationProfile == "" {
		run.VerificationProfile = firstConfiguredVerificationProfile(cfg)
	}
	if err := run.Validate(); err != nil {
		return RunRequest{}, err
	}
	// The run ID becomes a route segment and the artifact store's directory
	// name. Refuse an identity the store cannot address before starting work
	// that every later read would fail to find.
	if err := artifacts.ValidateSegment(run.RunID); err != nil {
		return RunRequest{}, fmt.Errorf("run_id %q must be one URL path segment", run.RunID)
	}
	return run, nil
}

// firstConfiguredHarness is the operator's default harness: the first
// configured name in stable order.
func firstConfiguredHarness(cfg Config) string {
	names := make([]string, 0, len(cfg.Harnesses))
	for name := range cfg.Harnesses {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// firstConfiguredVerificationProfile is the operator's default verification
// profile: the first configured name in stable order.
func firstConfiguredVerificationProfile(cfg Config) string {
	names := make([]string, 0, len(cfg.Verification))
	for name := range cfg.Verification {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}
