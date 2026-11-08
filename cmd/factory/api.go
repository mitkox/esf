package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"

	"github.com/spf13/cobra"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"github.com/mitkox/esf/internal/api"
	"github.com/mitkox/esf/internal/factory"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
)

// newAPICommand serves the public execution API.
//
// It is a separate listener from the read-only console and from the private sf
// turn executor: this is the only surface that can submit work.
func newAPICommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "api",
		Short: "Serve the public execution API",
		Long: `Serve the versioned execution API on the operator-configured listener.

The API accepts the same three caller inputs as the CLI (an approved repository,
an exact revision and task text) plus the names of operator-declared resources.
It never accepts an executable, a template id, a host path or a credential.

Authentication uses a bearer token file owned by the service user:

  [api]
  enabled = true
  listen = "127.0.0.1:8090"
  token_file = "/etc/factory/api.token"

A non-loopback listener requires native TLS. Use ` + "`factory api`" + ` with a
TLS-terminating proxy when the listener stays on loopback.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(command *cobra.Command, _ []string) error {
			ctx := command.Context()
			cfg, err := loadConfig(*configPath)
			if err != nil {
				return err
			}
			if !cfg.API.Enabled {
				return errors.New("the execution API is disabled; set [api].enabled = true in the factory configuration")
			}
			runtime, err := factory.NewRuntime(ctx, factory.RuntimeOptions{Config: cfg})
			if err != nil {
				return err
			}
			defer runtime.Close(ctx)
			temporalClient, err := runtime.TemporalClient()
			if err != nil {
				return err
			}
			defer temporalClient.Close()
			server, err := api.New(api.Config{
				Listen:      cfg.API.Listen,
				TokenFile:   cfg.API.TokenFile,
				TLSCertFile: cfg.API.TLSCertFile,
				TLSKeyFile:  cfg.API.TLSKeyFile,
				Controller: &apiController{
					config:    cfg,
					artifacts: runtime.Artifacts,
					client:    temporalClient,
				},
			})
			if err != nil {
				return err
			}
			scheme := "http"
			if cfg.API.TLSCertFile != "" || cfg.API.TLSKeyFile != "" {
				scheme = "https"
			}
			return server.Serve(ctx, func(address net.Addr) {
				fmt.Fprintf(command.OutOrStdout(), "factory api: serving %s://%s\n", scheme, address)
				fmt.Fprintln(command.OutOrStdout(), "factory api: bearer token required; the token is not printed")
			})
		},
	}
}

// apiController implements the execution API over Temporal and the durable
// artifact store. Submission uses the deterministic workflow ID so a repeated
// submission for one run identity starts one workflow.
type apiController struct {
	config    factory.Config
	artifacts artifacts.Factory
	client    client.Client

	// Seams for tests. Production leaves all nil and uses Temporal.
	startWorkflow func(ctx context.Context, options client.StartWorkflowOptions, run factory.RunRequest) error
	queryStatus   func(ctx context.Context, runID string) (factory.RunStatus, error)
	cancelRun     func(ctx context.Context, runID string) error
}

func (c *apiController) executeWorkflow(ctx context.Context, options client.StartWorkflowOptions, run factory.RunRequest) error {
	if c.startWorkflow != nil {
		return c.startWorkflow(ctx, options, run)
	}
	_, err := c.client.ExecuteWorkflow(ctx, options, factory.SoftwareChangeWorkflow, run)
	return err
}

func (c *apiController) cancelWorkflow(ctx context.Context, runID string) error {
	if c.cancelRun != nil {
		return c.cancelRun(ctx, runID)
	}
	return factory.CancelRun(ctx, c.client, runID)
}

func (c *apiController) lookupRunStatus(ctx context.Context, runID string) (factory.RunStatus, error) {
	if c.queryStatus != nil {
		return c.queryStatus(ctx, runID)
	}
	return factory.QueryRunStatus(ctx, c.client, factory.WorkflowIDForRun(runID), "")
}

func (c *apiController) runRequest(request api.RunRequest) (factory.RunRequest, error) {
	run := factory.RunRequest{
		RunID:               request.RunID,
		Repository:          request.Repository,
		RepositoryKind:      string(repository.SourceRemote),
		Revision:            request.Revision,
		Task:                request.Task,
		SandboxTemplate:     c.config.Cube.TemplateID,
		AgentHarness:        request.AgentHarness,
		AgentModel:          request.AgentModel,
		VerificationProfile: request.VerificationProfile,
		Scope:               request.Scope,
		Workspace:           request.Workspace,
		Model:               request.Model,
		EgressPolicy:        request.EgressPolicy,
		ChangeID:            request.ChangeID,
		ParentRunID:         request.ParentRunID,
		AgentTimeout:        c.config.Limits.AgentTimeout.Std(),
		VerificationTimeout: c.config.Limits.VerificationTimeout.Std(),
		TotalTimeout:        c.config.Limits.TotalTimeout.Std(),
	}
	if run.AgentHarness == "" {
		run.AgentHarness = firstHarnessName(c.config)
	}
	if run.VerificationProfile == "" {
		run.VerificationProfile = firstProfileName(c.config)
	}
	if err := run.Validate(); err != nil {
		return factory.RunRequest{}, fmt.Errorf("%w: %v", api.ErrInvalidRequest, err)
	}
	// The run ID becomes a route segment and the artifact store's directory
	// name. Refuse an identity the store cannot address before starting work
	// that every later read would fail to find.
	if err := artifacts.ValidateSegment(run.RunID); err != nil {
		return factory.RunRequest{}, fmt.Errorf("%w: run_id %q must be one URL path segment", api.ErrInvalidRequest, run.RunID)
	}
	return run, nil
}

func (c *apiController) Submit(ctx context.Context, request api.RunRequest) (api.RunStatus, error) {
	run, err := c.runRequest(request)
	if err != nil {
		return api.RunStatus{}, err
	}
	workflowID := factory.WorkflowIDForRun(run.RunID)
	err = c.executeWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: c.config.Temporal.TaskQueue,
		Memo:      factory.RunMemoFor(run),
		// A running workflow with this identity is reused, and a closed one is
		// not restarted. Together with the deterministic ID this makes a
		// repeated submission idempotent.
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	}, run)
	if err != nil {
		var started *serviceerror.WorkflowExecutionAlreadyStarted
		if errors.As(err, &started) {
			// With USE_EXISTING, an already-started result means the existing
			// execution is closed: retention is over and restart is refused.
			return api.RunStatus{}, fmt.Errorf("%w: run %s was already closed", api.ErrRunTerminal, run.RunID)
		}
		return api.RunStatus{}, fmt.Errorf("start run %s: %w", run.RunID, err)
	}
	// Report the run that actually owns the identity, never the newly
	// submitted request. A reused activation may be executing different inputs;
	// that is a conflict, not a silent restart.
	status, statusErr := c.lookupRunStatus(ctx, run.RunID)
	if statusErr == nil {
		if status.Repository != "" && status.Repository != run.Repository {
			return api.RunStatus{}, fmt.Errorf("%w: run %s is executing repository %q",
				api.ErrRunConflict, run.RunID, status.Repository)
		}
		if status.Revision != "" && status.Revision != run.Revision {
			return api.RunStatus{}, fmt.Errorf("%w: run %s is executing revision %q",
				api.ErrRunConflict, run.RunID, status.Revision)
		}
		if status.TaskHash != "" && status.TaskHash != repository.HashTask(run.Task) {
			return api.RunStatus{}, fmt.Errorf("%w: run %s is executing a different task",
				api.ErrRunConflict, run.RunID)
		}
		return statusFromRun(status), nil
	}
	// The workflow is new and not yet queryable. This answer is a minimal
	// acknowledgement; it carries no claim about the running execution.
	return api.RunStatus{
		RunID:               run.RunID,
		WorkflowID:          workflowID,
		State:               string(factory.StateRequested),
		Repository:          run.Repository,
		Revision:            run.Revision,
		Scope:               run.Scope,
		ChangeID:            run.ChangeID,
		AgentHarness:        run.AgentHarness,
		VerificationProfile: run.VerificationProfile,
	}, nil
}

func (c *apiController) Status(ctx context.Context, runID string) (api.RunStatus, error) {
	store, err := c.artifacts.ForRun(runID)
	if err != nil {
		return api.RunStatus{}, api.ErrRunNotFound
	}
	body, err := store.Read(factory.ArtifactRun)
	if err == nil {
		var manifest factory.RunManifest
		if err := json.Unmarshal(body, &manifest); err != nil {
			return api.RunStatus{}, fmt.Errorf("decode manifest for run %s: %w", runID, err)
		}
		return statusFromManifest(manifest), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return api.RunStatus{}, err
	}
	status, err := c.lookupRunStatus(ctx, runID)
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return api.RunStatus{}, api.ErrRunNotFound
		}
		return api.RunStatus{}, err
	}
	return statusFromRun(status), nil
}

func (c *apiController) Events(ctx context.Context, runID string, after int) ([]api.Event, error) {
	status, err := c.Status(ctx, runID)
	if err != nil {
		return nil, err
	}
	events := make([]api.Event, 0, len(status.Conditions))
	for index, condition := range status.Conditions {
		sequence := index + 1
		if sequence <= after {
			continue
		}
		events = append(events, api.Event{
			Seq:     sequence,
			Type:    condition.Type,
			Status:  condition.Status,
			Reason:  condition.Reason,
			Message: condition.Message,
			Time:    condition.LastTransitionTime,
		})
	}
	return events, nil
}

func (c *apiController) Evidence(_ context.Context, runID string) ([]byte, error) {
	store, err := c.artifacts.ForRun(runID)
	if err != nil {
		return nil, api.ErrRunNotFound
	}
	body, err := store.Read(factory.ArtifactRun)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, api.ErrRunNotFound
		}
		return nil, err
	}
	return body, nil
}

func (c *apiController) Cancel(ctx context.Context, runID string) (api.RunStatus, error) {
	if err := c.cancelWorkflow(ctx, runID); err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return api.RunStatus{}, api.ErrRunNotFound
		}
		return api.RunStatus{}, err
	}
	return c.Status(ctx, runID)
}

func statusFromRun(status factory.RunStatus) api.RunStatus {
	workflowID := factory.WorkflowIDForRun(status.RunID)
	return api.RunStatus{
		RunID:              status.RunID,
		WorkflowID:         workflowID,
		State:              string(status.State),
		CurrentStep:        status.CurrentStep,
		Repository:         status.Repository,
		Revision:           status.Revision,
		Scope:              status.Scope,
		ChangeID:           status.ChangeID,
		AgentResult:        string(status.AgentOutcome),
		VerificationResult: string(status.VerifyOutcome),
		StartedAt:          status.StartedAt,
		Conditions:         apiConditions(status.Conditions),
		Error:              status.Error,
	}
}

func statusFromManifest(manifest factory.RunManifest) api.RunStatus {
	workflowID := manifest.WorkflowID
	if workflowID == "" {
		workflowID = factory.WorkflowIDForRun(manifest.RunID)
	}
	return api.RunStatus{
		RunID:               manifest.RunID,
		WorkflowID:          workflowID,
		State:               string(manifest.FactoryResult),
		Repository:          manifest.Repository,
		Revision:            manifest.RequestedRevision,
		Scope:               manifest.Scope,
		ChangeID:            manifest.ChangeID,
		AgentHarness:        manifest.AgentHarness,
		VerificationProfile: manifest.VerificationProfile,
		AgentResult:         string(manifest.AgentResult),
		VerificationResult:  string(manifest.VerificationResult),
		StartedAt:           manifest.StartedAt,
		CompletedAt:         manifest.CompletedAt,
		Conditions:          apiConditions(manifest.Conditions),
		Error:               manifest.Error,
	}
}

func apiConditions(conditions []factory.Condition) []api.Condition {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]api.Condition, 0, len(conditions))
	for _, condition := range conditions {
		out = append(out, api.Condition{
			Type:               string(condition.Type),
			Status:             string(condition.Status),
			Reason:             condition.Reason,
			Message:            condition.Message,
			LastTransitionTime: condition.LastTransitionTime,
		})
	}
	return out
}
