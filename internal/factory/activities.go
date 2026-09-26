package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.temporal.io/sdk/activity"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/artifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/verification"
)

// Activities is the factory's side-effect layer.
//
// Every external operation lives here: network, filesystem, process and
// sandbox calls. Temporal workflow code never performs these directly, which is
// what makes the workflow deterministic and therefore durable.
type Activities struct {
	quality         *QualityRuntime
	provider        sandbox.Provider
	repos           *repository.Provider
	harnesses       *agentharness.Registry
	profiles        verification.Profiles
	artifactFactory artifacts.Factory
	redactor        *Redactor
	telemetry       *Telemetry
	cfg             Config
	log             *slog.Logger
}

// ActivitiesOptions configures the activity set.
type ActivitiesOptions struct {
	Quality         *QualityRuntime
	Provider        sandbox.Provider
	Repositories    *repository.Provider
	Harnesses       *agentharness.Registry
	Profiles        verification.Profiles
	ArtifactFactory artifacts.Factory
	Redactor        *Redactor
	Telemetry       *Telemetry
	Config          Config
	Logger          *slog.Logger
}

// NewActivities builds the activity set.
func NewActivities(opts ActivitiesOptions) (*Activities, error) {
	if opts.Provider == nil {
		return nil, fmt.Errorf("factory: sandbox provider is required")
	}
	if opts.Repositories == nil {
		return nil, fmt.Errorf("factory: repository provider is required")
	}
	if opts.Harnesses == nil {
		return nil, fmt.Errorf("factory: harness registry is required")
	}
	if opts.ArtifactFactory == nil {
		return nil, fmt.Errorf("factory: artifact factory is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	telemetry := opts.Telemetry
	if telemetry == nil {
		var err error
		telemetry, err = NewTelemetry(context.Background(), ObservabilityConfig{})
		if err != nil {
			return nil, err
		}
	}
	return &Activities{
		quality:         opts.Quality,
		provider:        opts.Provider,
		repos:           opts.Repositories,
		harnesses:       opts.Harnesses,
		profiles:        opts.Profiles,
		artifactFactory: opts.ArtifactFactory,
		redactor:        opts.Redactor,
		telemetry:       telemetry,
		cfg:             opts.Config,
		log:             log,
	}, nil
}

// ── Activity input/output types ──────────────────────────────────────────────
//
// Every activity takes and returns serializable values. Sandboxes are passed by
// ID, never as live handles, because each activity is a separate invocation.

// ValidateInput validates a run request against operator policy.
type ValidateInput struct {
	Request RunRequest
}

// ValidateOutput is the validated, canonicalised request.
type ValidateOutput struct {
	AgentTimeout        time.Duration `json:"agent_timeout"`
	VerificationTimeout time.Duration `json:"verification_timeout"`
	TotalTimeout        time.Duration `json:"total_timeout"`
	Repository          string        `json:"repository"`
	Kind                string        `json:"kind"`
	Revision            string        `json:"revision"`
	TaskHash            string        `json:"task_hash"`
	IntakeEnabled       bool          `json:"intake_enabled,omitempty"`
	// Resources is the fully-resolved resource set for the run. It is resolved
	// exactly once, here, and carried through the workflow as data: workflow
	// code must never resolve a name, because a config change during a run
	// would make Temporal replay non-deterministic.
	Resources ResolvedResources `json:"resources"`
}

// CreateSandboxInput creates the run's microVM.
type CreateSandboxInput struct {
	ExecutionID string         `json:"execution_id"`
	RunID       string         `json:"run_id"`
	Template    string         `json:"template"`
	Harness     string         `json:"harness"`
	IdleTimeout time.Duration  `json:"idle_timeout"`
	Limits      sandbox.Limits `json:"limits"`
	// Network is the effective egress policy resolved from the run's named
	// EgressPolicy. A zero value leaves the deployment default untouched.
	Network sandbox.Network `json:"network,omitempty"`
	// Env is operator-approved environment injected at creation.
	Env map[string]string `json:"env,omitempty"`
}

// CreateSandboxOutput identifies the created sandbox.
type CreateSandboxOutput struct {
	SandboxID string        `json:"sandbox_id"`
	Template  string        `json:"template"`
	Duration  time.Duration `json:"duration"`
}

// PrepareSandboxInput provisions the coding agent into the sandbox.
type PrepareSandboxInput struct {
	RunID     string `json:"run_id"`
	SandboxID string `json:"sandbox_id"`
	Harness   string `json:"harness"`
	// BasePackages overrides the factory-wide list when the run resolved a
	// named Workspace. Empty inherits the factory setting.
	BasePackages []string `json:"base_packages,omitempty"`
	// SetupScript overrides the factory-wide script when the run resolved a
	// named Workspace. Empty inherits the factory setting.
	SetupScript string `json:"setup_script,omitempty"`
	// WorkspaceResolved distinguishes a deliberately empty resolved value from
	// an older activity payload that predates declarative resources.
	WorkspaceResolved bool `json:"workspace_resolved,omitempty"`
	// ModelEndpoint is the non-secret endpoint configuration resolved during
	// validation. Credential values remain on the activity worker.
	ModelEndpoint agentharness.ModelEndpoint `json:"model_endpoint,omitempty"`
}

// PrepareSandboxOutput records what provisioning produced.
type PrepareSandboxOutput struct {
	HarnessVersion string        `json:"harness_version"`
	Duration       time.Duration `json:"duration"`
}

// PrepareRepositoryInput clones the repository into the sandbox.
type PrepareRepositoryInput struct {
	RunID     string     `json:"run_id"`
	SandboxID string     `json:"sandbox_id"`
	Request   RunRequest `json:"request"`
}

// PrepareRepositoryOutput records the baseline.
type PrepareRepositoryOutput struct {
	Baseline repository.Baseline `json:"baseline"`
}

// ApplyRuntimeNetworkInput locks down egress before untrusted agent execution.
type ApplyRuntimeNetworkInput struct {
	RunID     string `json:"run_id"`
	SandboxID string `json:"sandbox_id"`
	Harness   string `json:"harness"`
}

// ApplyRuntimeNetworkOutput records whether a harness-specific policy applied.
type ApplyRuntimeNetworkOutput struct {
	Applied bool `json:"applied"`
}

// RunAgentInput executes the coding agent inside the sandbox.
type RunAgentInput struct {
	RunID         string            `json:"run_id"`
	SandboxID     string            `json:"sandbox_id"`
	Harness       string            `json:"harness"`
	Model         string            `json:"model,omitempty"`
	Prompt        string            `json:"prompt"`
	RepositoryDir string            `json:"repository_dir"`
	Timeout       time.Duration     `json:"timeout"`
	Env           map[string]string `json:"env,omitempty"`
	// ModelAPIKeyEnv names an operator-approved host environment variable. The
	// value is read inside this activity and never travels through workflow
	// history or resolved-resource evidence.
	ModelAPIKeyEnv string `json:"model_api_key_env,omitempty"`
}

// RunAgentOutput records the agent outcome.
type RunAgentOutput struct {
	Result        agentharness.Result `json:"result"`
	Attempt       int32               `json:"attempt"`
	EvidenceError string              `json:"evidence_error,omitempty"`
}

// RunVerificationInput executes the deterministic gates.
type RunVerificationInput struct {
	Timeout       time.Duration `json:"timeout"`
	RunID         string        `json:"run_id"`
	SandboxID     string        `json:"sandbox_id"`
	Profile       string        `json:"profile"`
	RepositoryDir string        `json:"repository_dir"`
}

// RunVerificationOutput records the verification outcome.
type RunVerificationOutput struct {
	Result        verification.Result `json:"result"`
	EvidenceError string              `json:"evidence_error,omitempty"`
}

// CollectArtifactsInput extracts the patch and post-run state.
type CollectArtifactsInput struct {
	RunID         string `json:"run_id"`
	SandboxID     string `json:"sandbox_id"`
	RepositoryDir string `json:"repository_dir"`
	// Phase selects which artifact set is written.
	//
	// The empty phase is the AGENT's contribution — the deliverable — and is
	// captured immediately after the agent runs, BEFORE any verification gate
	// executes. VerificationPhase re-collects afterwards to detect whether a
	// gate mutated the working tree, which must never silently become part of
	// the patch: a build step that rewrites a lockfile with sandbox-local paths
	// would otherwise be attributed to the agent.
	Phase string `json:"phase,omitempty"`
}

// CollectArtifactsOutput records the extracted evidence.
type CollectArtifactsOutput struct {
	Patch         string `json:"patch"`
	PatchArtifact string `json:"patch_artifact"`
	ResultingSHA  string `json:"resulting_sha"`
}

// VerificationPhase is the CollectArtifacts phase run after verification.
const VerificationPhase = "post-verification"

// DestroySandboxInput tears the sandbox down.
type DestroySandboxInput struct {
	ExecutionID string `json:"execution_id"`
	RunID       string `json:"run_id"`
	SandboxID   string `json:"sandbox_id"`
}

// FinalizeInput writes the run manifest.
type FinalizeInput struct {
	Request                 RunRequest          `json:"request"`
	Validate                ValidateOutput      `json:"validate"`
	Intake                  *IntakeResult       `json:"intake,omitempty"`
	SandboxID               string              `json:"sandbox_id"`
	SandboxTemplate         string              `json:"sandbox_template"`
	HarnessVersion          string              `json:"harness_version"`
	AgentResult             agentharness.Result `json:"agent_result"`
	AgentOutcome            Outcome             `json:"agent_outcome"`
	AgentAttempt            int32               `json:"agent_attempt"`
	VerificationResult      verification.Result `json:"verification_result"`
	VerificationOutcome     Outcome             `json:"verification_outcome"`
	Baseline                repository.Baseline `json:"baseline"`
	Patch                   string              `json:"patch"`
	PatchArtifact           string              `json:"patch_artifact"`
	ResultingSHA            string              `json:"resulting_sha"`
	VerificationMutatedTree bool                `json:"verification_mutated_tree"`
	PostVerificationPatch   string              `json:"post_verification_patch"`
	FactoryResult           RunState            `json:"factory_result"`
	Cleanup                 CleanupResult       `json:"cleanup"`
	WorkflowID              string              `json:"workflow_id"`
	WorkflowRunID           string              `json:"workflow_run_id"`
	StartedAt               time.Time           `json:"started_at"`
	CompletedAt             time.Time           `json:"completed_at"`
	Error                   string              `json:"error,omitempty"`

	// ── AX-inspired fields ──────────────────────────────────────────────────
	// Conditions are the per-step outcomes derived by the workflow.
	Conditions []Condition `json:"conditions,omitempty"`
	// Resolved resources and their digests.
	Resources ResolvedResources `json:"resources"`
	// SuspendResult reports whether the review gate checkpointed the sandbox.
	SuspendResult Outcome `json:"suspend_result,omitempty"`
	SuspendError  string  `json:"suspend_error,omitempty"`
	PreviewResult Outcome `json:"preview_result,omitempty"`
	// ReviewConfigured and ReviewOutcome record the human gate, so "approved"
	// is distinguishable from "no review was configured".
	ReviewConfigured bool   `json:"review_configured,omitempty"`
	ReviewOutcome    string `json:"review_outcome,omitempty"`
	ReviewNote       string `json:"review_note,omitempty"`
	// ReviewError is a non-fatal review-gate problem (a preview that could not
	// be published), kept separate from SuspendError.
	ReviewError string `json:"review_error,omitempty"`
	// Previews are the ingress URLs exposed for review.
	Previews []PreviewLink `json:"previews,omitempty"`
	// BudgetExceeded lists the ceilings the run crossed, computed by the
	// workflow from recorded usage and the resolved budget.
	BudgetExceeded []string `json:"budget_exceeded,omitempty"`
}

// FinalizeOutput returns the persisted manifest.
type FinalizeOutput struct {
	Manifest RunManifest `json:"manifest"`
}

// ── Activities ───────────────────────────────────────────────────────────────

// ValidateRequest checks a request against operator policy and records it.
//
// It is a separate activity so that validation failures are visible in
// workflow history and so the same rules apply regardless of entry point.
func (a *Activities) ValidateRequest(ctx context.Context, in ValidateInput) (ValidateOutput, error) {
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanValidate)
	defer span.End()

	req := in.Request
	if err := req.Validate(); err != nil {
		return ValidateOutput{}, err
	}
	harness, err := a.harnesses.Resolve(req.AgentHarness)
	if err != nil {
		return ValidateOutput{}, fmt.Errorf("harness %q is not registered on this worker", req.AgentHarness)
	}
	if validator, ok := harness.(agentharness.ModelValidator); ok {
		if err := validator.ValidateModel(req.AgentModel); err != nil {
			return ValidateOutput{}, err
		}
	}
	if _, err := a.profiles.Resolve(req.VerificationProfile); err != nil {
		return ValidateOutput{}, err
	}

	kind := req.RepositoryKind
	if kind == "" {
		if req.LocalPath != "" {
			kind = string(repository.SourceLocal)
		} else {
			kind = string(repository.SourceRemote)
		}
	}
	// Repository policy is enforced here, before any sandbox exists.
	repoReq := repository.Request{
		Kind:      repository.Kind(kind),
		URL:       req.Repository,
		LocalPath: req.LocalPath,
		Revision:  req.Revision,
	}
	if err := a.repos.Validate(repoReq); err != nil {
		return ValidateOutput{}, err
	}

	// The manifest must name the exact source the agent started from, so a
	// local (bundle) source records its path too.
	source := req.Repository
	if source == "" {
		source = "local:" + req.LocalPath
	}

	// Resolve the named resources before anything else is accepted. This is the
	// only place resource names are resolved, and the result travels through the
	// workflow as plain data.
	resolved, err := a.cfg.ResolveResources(req)
	if err != nil {
		return ValidateOutput{}, fmt.Errorf("resolve resources: %w", err)
	}
	// A scope narrows the global allowlist; it never widens it. The check is
	// repeated here because the repository provider enforces only the global
	// policy.
	if req.Repository != "" && kind != string(repository.SourceLocal) {
		if !matchesAnyPrefix(req.Repository, resolved.RepositoriesAllowed) {
			return ValidateOutput{}, fmt.Errorf("repository %q is not allowed in scope %q", req.Repository, resolved.Scope)
		}
	}
	agentTimeout, err := boundedTimeout(req.AgentTimeout, a.cfg.Limits.AgentTimeout.Std(), 30*time.Minute)
	if err != nil {
		return ValidateOutput{}, fmt.Errorf("agent timeout: %w", err)
	}
	verifyTimeout, err := boundedTimeout(req.VerificationTimeout, a.cfg.Limits.VerificationTimeout.Std(), 15*time.Minute)
	if err != nil {
		return ValidateOutput{}, fmt.Errorf("verification timeout: %w", err)
	}
	totalTimeout, err := boundedTimeout(req.TotalTimeout, a.cfg.Limits.TotalTimeout.Std(), 90*time.Minute)
	if err != nil {
		return ValidateOutput{}, fmt.Errorf("total timeout: %w", err)
	}
	out := ValidateOutput{
		AgentTimeout: agentTimeout, VerificationTimeout: verifyTimeout, TotalTimeout: totalTimeout,
		Repository:    source,
		Kind:          kind,
		Revision:      req.Revision,
		TaskHash:      repository.HashTask(req.Task),
		IntakeEnabled: a.cfg.Intake.Enabled,
		Resources:     resolved,
	}
	span.SetAttributes(
		attribute.String(AttrRepository, out.Repository),
		attribute.String(AttrRevision, out.Revision),
	)

	// Persist the request itself: task.json is the record of what was asked.
	store, err := a.artifactFactory.ForRun(req.RunID)
	if err != nil {
		return ValidateOutput{}, err
	}
	record := map[string]any{
		"run_id":               req.RunID,
		"repository":           out.Repository,
		"repository_kind":      out.Kind,
		"requested_revision":   out.Revision,
		"task":                 req.Task,
		"task_hash":            out.TaskHash,
		"agent_harness":        req.AgentHarness,
		"agent_model":          req.AgentModel,
		"verification_profile": req.VerificationProfile,
		"sandbox_template":     req.SandboxTemplate,
		"scope":                resolved.Scope,
		"workspace":            resolved.Workspace,
		"egress_policy":        resolved.EgressPolicy,
		"model":                resolved.Model,
		"resources":            resolved,
		"recorded_at":          time.Now().UTC(),
	}
	if err := a.writeJSON(store, ArtifactTask, record); err != nil {
		return ValidateOutput{}, err
	}
	return out, nil
}

// CreateSandbox creates the run's isolated microVM.
func (a *Activities) CreateSandbox(ctx context.Context, in CreateSandboxInput) (CreateSandboxOutput, error) {
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanCubeCreate)
	defer span.End()

	template := in.Template
	if template == "" {
		template = a.cfg.Cube.TemplateID
	}
	spec := sandbox.Spec{
		Template: template,
		Metadata: map[string]string{
			// Tagging every factory sandbox makes leaks findable and ensures the
			// factory never confuses its own sandboxes with someone else's.
			"origin":          "factory",
			"run_id":          in.RunID,
			"harness":         in.Harness,
			"creator":         "factory",
			"workflow_run_id": in.ExecutionID,
		},
		IdleTimeout: in.IdleTimeout,
		Limits:      in.Limits,
		Env:         in.Env,
		Network:     in.Network,
	}

	// Recover an acknowledged or ambiguously completed create by ownership.
	// Never issue a second create after an ambiguous first attempt.
	infos, err := a.provider.List(ctx)
	if err != nil {
		return CreateSandboxOutput{}, fmt.Errorf("list before create: %w", err)
	}
	var owned []sandbox.Info
	for _, info := range infos {
		if ownsSandbox(info, in.RunID, in.ExecutionID) {
			owned = append(owned, info)
		}
	}
	if len(owned) > 1 {
		return CreateSandboxOutput{}, fmt.Errorf("multiple sandboxes belong to this execution")
	}
	if len(owned) == 1 {
		if owned[0].Template != template {
			return CreateSandboxOutput{}, fmt.Errorf("recovered sandbox template does not match")
		}
		return CreateSandboxOutput{SandboxID: owned[0].ID, Template: owned[0].Template}, nil
	}
	if attemptFromContext(ctx) > 1 {
		return CreateSandboxOutput{}, fmt.Errorf("previous sandbox create is unresolved; refusing duplicate creation")
	}

	if idleLimit := a.cfg.Limits.SandboxIdleTimeout.Std(); idleLimit > 0 && (spec.IdleTimeout <= 0 || spec.IdleTimeout > idleLimit) {
		spec.IdleTimeout = idleLimit
	}
	started := time.Now()
	sb, err := a.provider.Create(ctx, spec)
	if err != nil {
		span.RecordError(err)
		return CreateSandboxOutput{}, fmt.Errorf("create sandbox: %w", err)
	}
	out := CreateSandboxOutput{SandboxID: sb.ID(), Template: sb.Template(), Duration: time.Since(started)}

	if m := a.telemetry.Metrics(); m != nil {
		m.SandboxCreateDuration.Record(ctx, out.Duration.Seconds(),
			metric.WithAttributes(metricAttrs(a.provider.Name(), out.Template)...))
	}
	span.SetAttributes(
		attribute.String(AttrSandboxID, out.SandboxID),
		attribute.String(AttrSandboxTemplate, out.Template),
		attribute.String(AttrSandboxProvider, a.provider.Name()),
	)
	a.log.InfoContext(ctx, "sandbox created",
		slog.String("run.id", in.RunID),
		slog.String("sandbox.id", out.SandboxID),
		slog.String("sandbox.template", out.Template))
	return out, nil
}

// PrepareSandbox provisions the coding agent into the sandbox.
func (a *Activities) PrepareSandbox(ctx context.Context, in PrepareSandboxInput) (PrepareSandboxOutput, error) {
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanPrepareSandbox)
	defer span.End()

	harness, err := a.harnesses.Resolve(in.Harness)
	if err != nil {
		return PrepareSandboxOutput{}, err
	}
	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return PrepareSandboxOutput{}, err
	}

	started := time.Now()

	// Factory-owned base packages first. These are required by the factory
	// itself — git for cloning and diffing — so they must not depend on how a
	// particular harness was configured. A resolved Workspace may narrow the
	// list; it can never widen it to something the operator did not declare.
	basePackages := a.cfg.Sandbox.BasePackages
	if in.WorkspaceResolved {
		basePackages = in.BasePackages
	} else if len(in.BasePackages) > 0 {
		basePackages = in.BasePackages
	}
	if err := installBasePackages(ctx, sb, basePackages, a.log); err != nil {
		span.RecordError(err)
		return PrepareSandboxOutput{}, err
	}

	// Operator-configured environment setup that a package manager cannot
	// express. It runs before the harness because the harness may depend on it.
	setupScript := a.cfg.Sandbox.SetupScript
	if in.WorkspaceResolved {
		setupScript = in.SetupScript
	} else if strings.TrimSpace(in.SetupScript) != "" {
		setupScript = in.SetupScript
	}
	if script := strings.TrimSpace(setupScript); script != "" {
		exec, err := sb.Execute(ctx, sandbox.Command{
			Script:      script,
			Timeout:     20 * time.Minute,
			Description: "sandbox setup script",
		})
		if err != nil {
			span.RecordError(err)
			return PrepareSandboxOutput{}, fmt.Errorf("sandbox setup script: %w", err)
		}
		if !exec.Succeeded() {
			return PrepareSandboxOutput{}, fmt.Errorf("sandbox setup script failed (exit %d): %s",
				exec.ExitCode, truncateForError(exec.Stderr, 500))
		}
		a.log.InfoContext(ctx, "sandbox setup script completed")
	}

	if err := harness.Provision(ctx, sb); err != nil {
		span.RecordError(err)
		return PrepareSandboxOutput{}, fmt.Errorf("provision harness %q: %w", in.Harness, err)
	}
	if configurer, ok := harness.(agentharness.ModelEndpointConfigurer); ok {
		if err := configurer.ConfigureModelEndpoint(ctx, sb, in.ModelEndpoint); err != nil {
			span.RecordError(err)
			return PrepareSandboxOutput{}, fmt.Errorf("configure model endpoint for harness %q: %w", in.Harness, err)
		}
	} else if in.ModelEndpoint.BaseURL != "" {
		return PrepareSandboxOutput{}, fmt.Errorf("configure model endpoint for harness %q: base_url is unsupported", in.Harness)
	}
	out := PrepareSandboxOutput{Duration: time.Since(started)}

	// Version detection is best-effort and never fails a run.
	if versioner, ok := harness.(agentharness.Versioner); ok {
		out.HarnessVersion = versioner.Version(ctx, sb)
	}
	a.log.InfoContext(ctx, "harness provisioned",
		slog.String("run.id", in.RunID),
		slog.String("harness", in.Harness),
		slog.String("harness.version", out.HarnessVersion),
		slog.Duration("duration", out.Duration))
	return out, nil
}

// PrepareRepository clones the repository and records the baseline.
func (a *Activities) PrepareRepository(ctx context.Context, in PrepareRepositoryInput) (PrepareRepositoryOutput, error) {
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanRepoPrepare)
	defer span.End()

	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return PrepareRepositoryOutput{}, err
	}

	kind := in.Request.RepositoryKind
	if kind == "" {
		if in.Request.LocalPath != "" {
			kind = string(repository.SourceLocal)
		} else {
			kind = string(repository.SourceRemote)
		}
	}
	baseline, err := a.repos.Prepare(ctx, sb, repository.Request{
		Kind:      repository.Kind(kind),
		URL:       in.Request.Repository,
		LocalPath: in.Request.LocalPath,
		Revision:  in.Request.Revision,
		DestDir:   in.Request.EffectiveRepositoryDir(),
	})
	if err != nil {
		span.RecordError(err)
		return PrepareRepositoryOutput{}, err
	}
	span.SetAttributes(attribute.String("repository.sha", baseline.SHA))

	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		return PrepareRepositoryOutput{}, err
	}
	if err := a.writeJSON(store, ArtifactBaseline, baseline); err != nil {
		return PrepareRepositoryOutput{}, err
	}
	// git-before.txt answers "what exact code did the agent start from?" in the
	// most direct way possible.
	if err := store.Write(ArtifactGitBefore, []byte(fmt.Sprintf("sha=%s\nbranch=%s\nstatus:\n%s\n",
		baseline.SHA, baseline.Branch, baseline.Status))); err != nil {
		return PrepareRepositoryOutput{}, err
	}
	a.log.InfoContext(ctx, "repository prepared",
		slog.String("run.id", in.RunID),
		slog.String("repository.sha", baseline.SHA))
	return PrepareRepositoryOutput{Baseline: baseline}, nil
}

// ApplyRuntimeNetwork replaces setup-time networking with the agent policy.
// The provider secret is resolved from the worker's secret source here, so it is
// never serialized through Temporal or made visible inside the sandbox.
func (a *Activities) ApplyRuntimeNetwork(ctx context.Context, in ApplyRuntimeNetworkInput) (ApplyRuntimeNetworkOutput, error) {
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanNetworkLockdown)
	defer span.End()

	network, applies, err := a.cfg.runtimeNetworkForHarness(in.Harness)
	if err != nil {
		span.RecordError(err)
		return ApplyRuntimeNetworkOutput{}, err
	}
	if !applies {
		return ApplyRuntimeNetworkOutput{}, nil
	}
	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return ApplyRuntimeNetworkOutput{}, err
	}
	updater, ok := sb.(sandbox.NetworkUpdater)
	if !ok {
		return ApplyRuntimeNetworkOutput{}, fmt.Errorf("sandbox provider %q cannot apply the required runtime network policy", a.provider.Name())
	}
	if err := updater.UpdateNetwork(ctx, network); err != nil {
		span.RecordError(err)
		return ApplyRuntimeNetworkOutput{}, fmt.Errorf("apply runtime network policy: %w", err)
	}
	a.log.InfoContext(ctx, "sandbox runtime network locked down",
		slog.String("run.id", in.RunID),
		slog.String("harness", in.Harness),
		slog.Int("additional_allow_out", len(network.AllowOut)))
	return ApplyRuntimeNetworkOutput{Applied: true}, nil
}

// RunAgent executes the coding agent inside the sandbox.
//
// Temporal's attempt number is recorded so that a retried agent run is visible
// in evidence as a distinct attempt rather than silently overwriting the first.
func (a *Activities) RunAgent(ctx context.Context, in RunAgentInput) (RunAgentOutput, error) {
	stopHeartbeat := startActivityHeartbeat(ctx)
	defer stopHeartbeat()
	attempt := int32(attemptFromContext(ctx))
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanAgentRun)
	defer span.End()

	harness, err := a.harnesses.Resolve(in.Harness)
	if err != nil {
		return RunAgentOutput{}, err
	}
	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return RunAgentOutput{}, err
	}

	env := make(map[string]string, len(in.Env)+1)
	for name, value := range in.Env {
		env[name] = value
	}
	if name := strings.TrimSpace(in.ModelAPIKeyEnv); name != "" {
		value := os.Getenv(name)
		if value == "" {
			return RunAgentOutput{}, fmt.Errorf("resolved model credential environment variable %s is empty", name)
		}
		env[name] = value
	}

	result, err := harness.Run(ctx, sb, agentharness.Task{
		RunID:         in.RunID,
		Prompt:        in.Prompt,
		RepositoryDir: in.RepositoryDir,
		Model:         in.Model,
		Timeout:       in.Timeout,
		Env:           env,
	})
	if err != nil {
		span.RecordError(err)
		return RunAgentOutput{}, fmt.Errorf("run agent: %w", err)
	}

	// Persist agent evidence immediately, redacted. A failed agent's output is
	// exactly when the evidence matters most.
	store, evidenceErr := a.artifactFactory.ForRun(in.RunID)
	if evidenceErr == nil {
		evidenceErr = errors.Join(
			store.Write(ArtifactAgentStdout, a.redactBytesString(result.Stdout)),
			store.Write(ArtifactAgentStderr, a.redactBytesString(result.Stderr)),
			store.Write(ArtifactAgentPrompt, a.redactBytesString(in.Prompt)),
			a.writeJSON(store, ArtifactAgentResult, map[string]any{"result": result, "attempt": attempt}),
		)
	}
	evidenceError := ""
	if evidenceErr != nil {
		evidenceError = a.redactor.Redact(evidenceErr.Error())
	}

	if m := a.telemetry.Metrics(); m != nil {
		m.AgentDurationSeconds.Record(ctx, result.Duration.Seconds(),
			metric.WithAttributes(metricAttrs(a.provider.Name(), in.Harness)...))
	}
	span.SetAttributes(
		attribute.String(AttrAgentHarness, in.Harness),
		attribute.Int(AttrAgentResult, result.ExitCode),
	)
	a.log.InfoContext(ctx, "agent finished",
		slog.String("run.id", in.RunID),
		slog.String("harness", in.Harness),
		slog.Int("exit_code", result.ExitCode),
		slog.Bool("timed_out", result.TimedOut),
		slog.Int64("attempt", int64(attempt)),
		slog.Duration("duration", result.Duration))
	return RunAgentOutput{Result: result, Attempt: attempt, EvidenceError: evidenceError}, nil
}

// RunVerification executes the deterministic gates.
func (a *Activities) RunVerification(ctx context.Context, in RunVerificationInput) (RunVerificationOutput, error) {
	timeout, err := boundedTimeout(in.Timeout, a.cfg.Limits.VerificationTimeout.Std(), 15*time.Minute)
	if err != nil {
		return RunVerificationOutput{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx, span := a.telemetry.Tracer().Start(ctx, "verify."+in.Profile)
	defer span.End()

	profile, err := a.profiles.Resolve(in.Profile)
	if err != nil {
		return RunVerificationOutput{}, err
	}
	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return RunVerificationOutput{}, err
	}
	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		return RunVerificationOutput{}, err
	}

	// Output is redacted on the way to storage; verification logs can contain
	// anything the repository prints, including a token it read from its env.
	sink := &redactingSink{store: store, redactor: a.redactor}
	runner := &verification.Runner{
		Sink:           sink,
		ArtifactPrefix: "verification",
	}
	result, err := runner.Run(ctx, sb, profile, in.RepositoryDir)
	if err != nil {
		span.RecordError(err)
		return RunVerificationOutput{}, err
	}
	evidenceErr := errors.Join(sink.err, a.writeJSON(store, ArtifactVerificationResult, result))
	evidenceError := ""
	if evidenceErr != nil {
		evidenceError = a.redactor.Redact(evidenceErr.Error())
	}

	if m := a.telemetry.Metrics(); m != nil {
		m.VerifyDurationSeconds.Record(ctx, result.Duration.Seconds(),
			metric.WithAttributes(metricAttrs(a.provider.Name(), in.Profile)...))
	}
	span.SetAttributes(
		attribute.Bool("verification.passed", result.Passed),
		attribute.Int("verification.steps", len(result.Steps)),
	)
	a.log.InfoContext(ctx, "verification finished",
		slog.String("run.id", in.RunID),
		slog.String("profile", in.Profile),
		slog.Bool("passed", result.Passed),
		slog.Duration("duration", result.Duration))
	return RunVerificationOutput{Result: result, EvidenceError: evidenceError}, nil
}

// CollectArtifacts extracts the patch and the post-run repository state.
func (a *Activities) CollectArtifacts(ctx context.Context, in CollectArtifactsInput) (CollectArtifactsOutput, error) {
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanArtifacts)
	defer span.End()

	sb, err := a.provider.Reattach(ctx, in.SandboxID)
	if err != nil {
		return CollectArtifactsOutput{}, err
	}
	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		return CollectArtifactsOutput{}, err
	}

	out := CollectArtifactsOutput{}

	patchArtifact := ArtifactPatch
	statusArtifact := ArtifactGitAfter
	if in.Phase == VerificationPhase {
		patchArtifact = ArtifactPostVerificationPatch
		statusArtifact = ArtifactPostVerificationStatus
	}

	// An empty patch is valid, but a failed extraction is missing evidence.
	patch, err := a.repos.Patch(ctx, sb, in.RepositoryDir)
	if err != nil {
		span.RecordError(err)
		return CollectArtifactsOutput{}, fmt.Errorf("extract patch: %w", err)
	}
	out.Patch = patch
	if err := store.Write(patchArtifact, a.redactBytesString(patch)); err != nil {
		return CollectArtifactsOutput{}, err
	}
	out.PatchArtifact = patchArtifact

	out.ResultingSHA, err = a.repos.HeadSHA(ctx, sb, in.RepositoryDir)
	if err != nil {
		return CollectArtifactsOutput{}, err
	}
	status, err := a.repos.Status(ctx, sb, in.RepositoryDir)
	if err != nil {
		return CollectArtifactsOutput{}, err
	}
	if err := store.Write(statusArtifact, a.redactBytesString(fmt.Sprintf("sha=%s\nstatus:\n%s\n", out.ResultingSHA, status))); err != nil {
		return CollectArtifactsOutput{}, err
	}

	a.log.InfoContext(ctx, "artifacts collected",
		slog.String("run.id", in.RunID),
		slog.String("phase", in.Phase),
		slog.Int("patch_bytes", len(out.Patch)),
		slog.String("resulting_sha", out.ResultingSHA))
	return out, nil
}

// DestroySandbox tears the sandbox down and verifies that it is gone.
//
// Cleanup is an activity rather than inlined workflow code so it is retried on
// transient failure and appears in workflow history.
func (a *Activities) DestroySandbox(ctx context.Context, in DestroySandboxInput) (CleanupResult, error) {
	ctx, span := a.telemetry.Tracer().Start(ctx, SpanCubeDestroy)
	defer span.End()

	result := CleanupResult{
		Attempted:  true,
		SandboxID:  in.SandboxID,
		FinishedAt: time.Now().UTC(),
	}
	started := time.Now()

	infos, err := a.provider.List(ctx)
	if err != nil {
		return result, fmt.Errorf("list before cleanup: %w", err)
	}
	ids := map[string]bool{}
	if in.SandboxID != "" {
		ids[in.SandboxID] = true
	}
	for _, info := range infos {
		if ownsSandbox(info, in.RunID, in.ExecutionID) {
			ids[info.ID] = true
		}
	}
	var destroyErrors []error
	for id := range ids {
		if err := a.provider.Destroy(ctx, id); err != nil {
			destroyErrors = append(destroyErrors, fmt.Errorf("destroy %s: %w", id, err))
		}
	}
	if err := errors.Join(destroyErrors...); err != nil {
		return result, err
	}
	infos, err = a.provider.List(ctx)
	if err != nil {
		return result, fmt.Errorf("verify cleanup: %w", err)
	}
	for _, info := range infos {
		if ids[info.ID] || ownsSandbox(info, in.RunID, in.ExecutionID) {
			result.Remaining++
		}
	}
	if result.Remaining > 0 {
		result.Outcome = OutcomeFailed
		if m := a.telemetry.Metrics(); m != nil {
			m.SandboxLeaksTotal.Add(ctx, 1, metric.WithAttributes(metricAttrs(a.provider.Name(), "")...))
		}
		return result, fmt.Errorf("%d execution sandboxes remain after cleanup", result.Remaining)
	}
	result.Duration = time.Since(started)
	result.FinishedAt = time.Now().UTC()

	result.Outcome = OutcomeSuccess
	result.Verified = true

	// Record the cleanup outcome durably.
	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		return result, err
	}
	if err := a.writeJSON(store, ArtifactCleanup, result); err != nil {
		return result, err
	}
	span.SetAttributes(attribute.String(AttrCleanupResult, string(result.Outcome)))
	a.log.InfoContext(ctx, "sandbox destroyed and verified",
		slog.String("run.id", in.RunID),
		slog.String("sandbox.id", in.SandboxID),
		slog.Int("remaining", result.Remaining))
	return result, nil
}

// FinalizeResult writes the durable run manifest.
func (a *Activities) FinalizeResult(ctx context.Context, in FinalizeInput) (FinalizeOutput, error) {
	store, err := a.artifactFactory.ForRun(in.Request.RunID)
	if err != nil {
		return FinalizeOutput{}, err
	}

	manifest := RunManifest{
		RunID:          in.Request.RunID,
		ChangeID:       in.Request.ChangeID,
		ParentRunID:    in.Request.ParentRunID,
		Scope:          in.Resources.Scope,
		FactoryVersion: a.cfg.effectiveVersion(),
		WorkflowID:     in.WorkflowID,
		WorkflowRunID:  in.WorkflowRunID,

		Repository:              in.Validate.Repository,
		RepositoryKind:          in.Validate.Kind,
		RequestedRevision:       in.Validate.Revision,
		BaselineSHA:             in.Baseline.SHA,
		ResultingSHA:            in.ResultingSHA,
		TaskHash:                in.Validate.TaskHash,
		AgentHarness:            in.Request.AgentHarness,
		AgentVersion:            in.HarnessVersion,
		SandboxProvider:         a.provider.Name(),
		CubeVersion:             a.cfg.Cube.Version,
		SandboxTemplate:         in.SandboxTemplate,
		SandboxID:               in.SandboxID,
		VerificationProfile:     in.Request.VerificationProfile,
		StartedAt:               in.StartedAt,
		CompletedAt:             in.CompletedAt,
		Duration:                in.CompletedAt.Sub(in.StartedAt),
		AgentResult:             in.AgentOutcome,
		AgentExitCode:           in.AgentResult.ExitCode,
		VerificationResult:      in.VerificationOutcome,
		FactoryResult:           in.FactoryResult,
		CleanupResult:           in.Cleanup,
		Artifacts:               store.Location(),
		Patch:                   in.PatchArtifact,
		Error:                   a.redactor.Redact(in.Error),
		VerificationMutatedTree: in.VerificationMutatedTree,
		PostVerificationPatch:   in.PostVerificationPatch,
		Workspace:               in.Resources.Workspace,
		WorkspaceDigest:         in.Resources.WorkspaceDigest,
		EgressPolicy:            in.Resources.EgressPolicy,
		EgressDigest:            in.Resources.EgressDigest,
		EgressSummary:           in.Resources.EgressSummary,
		ModelDigest:             in.Resources.ModelDigest,
		ResourceDigest:          in.Resources.Digest(),
		Conditions:              in.Conditions,
		SuspendResult:           in.SuspendResult,
		SuspendError:            a.redactor.Redact(in.SuspendError),
		PreviewResult:           in.PreviewResult,
		ReviewConfigured:        in.ReviewConfigured,
		ReviewOutcome:           in.ReviewOutcome,
		ReviewNote:              a.redactor.Redact(in.ReviewNote),
		ReviewError:             a.redactor.Redact(in.ReviewError),
		Previews:                in.Previews,
		BudgetExceeded:          in.BudgetExceeded,
		Intake:                  in.Intake,
		// HumanResult is the same value under its Phase-2 name: it drives the
		// change lifecycle, where an approval closes the change and a rejection
		// returns it to OPEN.
		HumanResult:   in.ReviewOutcome,
		TokensIn:      in.AgentResult.TokensIn,
		TokensOut:     in.AgentResult.TokensOut,
		InferenceCost: in.AgentResult.CostUSD,
	}
	// The model is reported from two directions: the run's resolved resource
	// (authoritative, operator-declared) and the harness's own report (what it
	// actually used). The resolved value wins for identity, because a harness
	// that ignores its configuration must not be able to change the record.
	if in.Resources.ModelID != "" {
		manifest.Model = in.Resources.ModelID
		manifest.ModelProvider = in.Resources.ModelProvider
	} else {
		manifest.Model = in.AgentResult.Model
	}

	if in.Patch != "" && in.PatchArtifact == "" {
		// Ensure the patch exists even if patch extraction was retried.
		if err := store.Write(ArtifactPatch, a.redactBytesString(in.Patch)); err != nil {
			return FinalizeOutput{}, err
		}
		manifest.Patch = ArtifactPatch
	}

	if err := a.writeJSON(store, ArtifactRun, manifest); err != nil {
		return FinalizeOutput{}, err
	}

	if m := a.telemetry.Metrics(); m != nil {
		m.RecordRun(ctx, manifest.FactoryResult, manifest.Duration,
			metricAttrs(a.provider.Name(), in.Request.AgentHarness)...)
	}
	a.log.InfoContext(ctx, "run finalized",
		slog.String("run.id", manifest.RunID),
		slog.String("factory.result", string(manifest.FactoryResult)),
		slog.String("agent.result", string(manifest.AgentResult)),
		slog.String("verification.result", string(manifest.VerificationResult)),
		slog.String("cleanup.result", string(manifest.CleanupResult.Outcome)))
	return FinalizeOutput{Manifest: manifest}, nil
}

// WriteEnvironment records the execution environment for reproducibility.
//
// It is invoked by the worker during setup rather than as a Temporal activity,
// because environment facts are static for the life of the worker.
func (a *Activities) WriteEnvironment(ctx context.Context, runID string, extra map[string]any) error {
	store, err := a.artifactFactory.ForRun(runID)
	if err != nil {
		return err
	}
	record := map[string]any{
		"factory_version":  a.cfg.effectiveVersion(),
		"sandbox_provider": a.provider.Name(),
		"cube_api_url":     a.cfg.Cube.APIURL,
		"cube_version":     a.cfg.Cube.Version,
		"sandbox_template": a.cfg.Cube.TemplateID,
		"capabilities":     a.provider.Capabilities(),
		"harnesses":        a.harnesses.Names(),
		"recorded_at":      time.Now().UTC(),
	}
	for k, v := range extra {
		record[k] = v
	}
	return a.writeJSON(store, ArtifactEnvironment, record)
}

// installBasePackages installs the factory's own prerequisites in the sandbox.
//
// It is idempotent, so a retried PrepareSandbox activity is safe.
func installBasePackages(ctx context.Context, sb sandbox.Sandbox, packages []string, log *slog.Logger) error {
	if len(packages) == 0 {
		return nil
	}
	script := "set -eu\n" +
		"export DEBIAN_FRONTEND=noninteractive\n" +
		"if command -v apt-get >/dev/null 2>&1; then\n" +
		"  apt-get update -qq\n" +
		"  apt-get install -y -qq --no-install-recommends " + strings.Join(packages, " ") + " >/dev/null 2>&1\n" +
		"fi\n" +
		"echo base-packages-ok\n"

	exec, err := sb.Execute(ctx, sandbox.Command{
		Script:      script,
		Timeout:     10 * time.Minute,
		Description: "install factory base packages: " + strings.Join(packages, " "),
	})
	if err != nil {
		return fmt.Errorf("install base packages: %w", err)
	}
	if !exec.Succeeded() {
		return fmt.Errorf("install base packages %v failed (exit %d): %s",
			packages, exec.ExitCode, truncateForError(exec.Stderr, 400))
	}
	log.InfoContext(ctx, "factory base packages installed", slog.Any("packages", packages))
	return nil
}

func truncateForError(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ── helpers ──────────────────────────────────────────────────────────────────

// redactingSink applies redaction before delegating to the artifact store.
type redactingSink struct {
	store    artifacts.Store
	redactor *Redactor
	err      error
}

func (s *redactingSink) Write(relPath string, data []byte) error {
	err := s.store.Write(relPath, s.redactor.RedactBytes(data))
	s.err = errors.Join(s.err, err)
	return err
}

func (a *Activities) writeJSON(store artifacts.Store, relPath string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", relPath, err)
	}
	// Redact decoded strings, then encode again so quotes and newlines in
	// secrets cannot escape redaction or damage the JSON structure.
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode %s for redaction: %w", relPath, err)
	}
	data, err = json.MarshalIndent(a.redactJSON(decoded), "", "  ")
	if err != nil {
		return fmt.Errorf("encode redacted %s: %w", relPath, err)
	}
	data = append(data, '\n')
	if err := store.Write(relPath, data); err != nil {
		return err
	}
	return nil
}

func (a *Activities) redactJSON(value any) any {
	switch v := value.(type) {
	case string:
		return a.redactor.Redact(v)
	case map[string]any:
		for key, child := range v {
			v[key] = a.redactJSON(child)
		}
	case []any:
		for i, child := range v {
			v[i] = a.redactJSON(child)
		}
	}
	return value
}

func (a *Activities) redactBytesString(s string) []byte {
	return a.redactor.RedactBytes([]byte(s))
}

// effectiveVersion returns the version recorded in evidence.
func (c Config) effectiveVersion() string {
	if strings.TrimSpace(c.FactoryVersion) != "" {
		return c.FactoryVersion
	}
	return Version
}

// attemptFromContext extracts the Temporal activity attempt number without
// importing Temporal into this file's dependency graph at call sites.
func attemptFromContext(ctx context.Context) int {
	if activity.IsActivity(ctx) {
		return int(activity.GetInfo(ctx).Attempt)
	}
	return 1
}

// metricAttrs builds the standard low-cardinality attribute set for a metric
// sample.
func metricAttrs(provider, harness string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String(AttrSandboxProvider, provider)}
	if harness != "" {
		attrs = append(attrs, attribute.String(AttrAgentHarness, harness))
	}
	return attrs
}

// Execution IDs prevent a later submission using the same run ID from adopting
// or destroying an earlier execution's sandbox. Empty IDs never match metadata.
func ownsSandbox(info sandbox.Info, runID, executionID string) bool {
	return executionID != "" && info.Metadata["origin"] == "factory" &&
		info.Metadata["run_id"] == runID && info.Metadata["workflow_run_id"] == executionID
}

func boundedTimeout(requested, limit, fallback time.Duration) (time.Duration, error) {
	if limit <= 0 {
		limit = fallback
	}
	if requested < 0 || requested > limit {
		return 0, fmt.Errorf("must be positive and at most %s", limit)
	}
	if requested == 0 {
		return limit, nil
	}
	return requested, nil
}
