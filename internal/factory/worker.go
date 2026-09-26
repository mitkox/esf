package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/artifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/verification"
)

// Runtime bundles the wired-up dependencies of a factory process.
//
// Constructing it validates configuration and connects nothing: Run connects.
type Runtime struct {
	Quality   *QualityRuntime
	Config    Config
	Provider  sandbox.Provider
	Repos     *repository.Provider
	Harnesses *agentharness.Registry
	Profiles  verification.Profiles
	Artifacts artifacts.Factory
	Redactor  *Redactor
	Telemetry *Telemetry
	Log       *slog.Logger
}

// RuntimeOptions configures construction.
type RuntimeOptions struct {
	Config Config
	// Logger defaults to a text logger on stderr.
	Logger *slog.Logger
	// SandboxProvider overrides the default CubeSandbox provider. Tests inject
	// a fake here so the whole runtime can be exercised without Cube.
	SandboxProvider sandbox.Provider
}

// NewRuntime builds a Runtime from configuration.
//
// Startup validation lives here: a missing Cube URL, an unknown harness type or
// a malformed verification profile fails immediately and loudly rather than
// halfway through a run.
func NewRuntime(ctx context.Context, opts RuntimeOptions) (*Runtime, error) {
	cfg := opts.Config
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	harnesses, err := cfg.BuildHarnesses()
	if err != nil {
		return nil, fmt.Errorf("build harnesses: %w", err)
	}

	provider := opts.SandboxProvider
	if provider == nil {
		cubeProvider, err := cube.New(cfg.Cube, cube.WithLogger(log))
		if err != nil {
			return nil, fmt.Errorf("build cubesandbox provider: %w", err)
		}
		provider = cubeProvider
	}

	artifactFactory, err := artifacts.NewLocal(cfg.Storage.DataDir)
	if err != nil {
		return nil, fmt.Errorf("build artifact store: %w", err)
	}

	telemetry, err := NewTelemetry(ctx, cfg.Observability)
	if err != nil {
		return nil, fmt.Errorf("build telemetry: %w", err)
	}

	// The redactor is seeded with secrets the factory itself handles, so they
	// can never be persisted into evidence even if the agent echoes them.
	redactor := NewRedactor(collectSecrets(cfg, harnesses)...)

	return &Runtime{
		Config:    cfg,
		Provider:  provider,
		Repos:     repository.New(cfg.Repositories.Allowed, repository.WithLogger(log)),
		Harnesses: harnesses,
		Profiles:  cfg.VerificationProfiles(),
		Artifacts: artifactFactory,
		Redactor:  redactor,
		Telemetry: telemetry,
		Log:       log,
	}, nil
}

// Close releases resources held by the runtime.
func (r *Runtime) Close(ctx context.Context) error {
	var firstErr error
	if r.Quality != nil {
		firstErr = r.Quality.Store.Close()
	}
	if closer, ok := r.Provider.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			firstErr = err
		}
	}
	if err := r.Telemetry.Shutdown(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// Activities builds the activity set for this runtime.
func (r *Runtime) Activities() (*Activities, error) {
	return NewActivities(ActivitiesOptions{
		Quality:         r.Quality,
		Provider:        r.Provider,
		Repositories:    r.Repos,
		Harnesses:       r.Harnesses,
		Profiles:        r.Profiles,
		ArtifactFactory: r.Artifacts,
		Redactor:        r.Redactor,
		Telemetry:       r.Telemetry,
		Config:          r.Config,
		Logger:          r.Log,
	})
}

// TemporalClient dials Temporal. The caller must Close the returned client.
func (r *Runtime) TemporalClient() (client.Client, error) {
	connection, err := r.Config.Temporal.connectionOptions()
	if err != nil {
		return nil, err
	}
	opts := client.Options{
		HostPort:  r.Config.Temporal.HostPort,
		Namespace: r.Config.Temporal.Namespace,
		// Identity makes it obvious in the Temporal UI which worker handled a
		// task, and distinguishes concurrent workers on one host.
		Identity:          workerIdentity(r.Config.Temporal.IdentityPrefix),
		Logger:            newTemporalLogger(r.Log),
		ConnectionOptions: connection,
	}
	if name := r.Config.Temporal.APIKeyEnv; name != "" {
		opts.Credentials = client.NewAPIKeyStaticCredentials(os.Getenv(name))
	}
	if path := r.Config.Temporal.PayloadKeyring; path != "" {
		codec, err := loadPayloadCodec(path)
		if err != nil {
			return nil, err
		}
		opts.DataConverter = converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
		opts.FailureConverter = temporal.NewDefaultFailureConverter(temporal.DefaultFailureConverterOptions{EncodeCommonAttributes: true, DataConverter: opts.DataConverter})
	}
	c, err := client.Dial(opts)
	if err != nil {
		return nil, fmt.Errorf("dial temporal at %s: %w", r.Config.Temporal.HostPort, err)
	}
	return c, nil
}

// RunWorker starts the Temporal worker and blocks until ctx is cancelled.
func (r *Runtime) RunWorker(ctx context.Context) error {
	if err := r.openQuality(); err != nil {
		return err
	}
	acts, err := r.Activities()
	if err != nil {
		return err
	}

	temporalClient, err := r.TemporalClient()
	if err != nil {
		return err
	}
	defer temporalClient.Close()

	fatalErrors := make(chan error, 1)
	w := worker.New(temporalClient, r.Config.Temporal.TaskQueue, worker.Options{
		Identity: workerIdentity(r.Config.Temporal.IdentityPrefix),
		OnFatalError: func(err error) {
			select {
			case fatalErrors <- err:
			default:
			}
		},
		// One sandbox per run; the MVP does not need aggressive concurrency and
		// a bounded limit keeps resource use predictable.
		WorkerStopTimeout:                      30 * time.Second,
		MaxConcurrentActivityExecutionSize:     8,
		MaxConcurrentWorkflowTaskExecutionSize: 8,
	})

	w.RegisterWorkflowWithOptions(SoftwareChangeWorkflow, workflowRegistrationOptions())
	w.RegisterWorkflow(CAPAWorkflow)

	// RegisterActivity panics on a bad method set, so it has no error return.
	w.RegisterActivity(acts)

	// Verify Cube is reachable before declaring the worker ready. A worker that
	// accepts work it cannot execute is worse than one that fails loudly.
	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := r.Provider.Ping(pingCtx); err != nil {
		return fmt.Errorf("sandbox provider is not usable: %w", err)
	}
	r.Log.Info("factory worker starting",
		slog.String("temporal.address", r.Config.Temporal.HostPort),
		slog.String("temporal.namespace", r.Config.Temporal.Namespace),
		slog.String("task_queue", r.Config.Temporal.TaskQueue),
		slog.String("sandbox.provider", r.Provider.Name()),
		slog.String("sandbox.template", r.Config.Cube.TemplateID),
		slog.Any("harnesses", r.Harnesses.Names()),
		slog.String("artifacts", r.Artifacts.Root()),
	)

	if err := w.Start(); err != nil {
		return fmt.Errorf("temporal worker stopped: %w", err)
	}
	defer w.Stop()
	if r.Quality != nil {
		stop, err := r.serveQuality(ctx, temporalClient)
		if err != nil {
			return err
		}
		defer stop()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-fatalErrors:
		return fmt.Errorf("temporal worker fatal error: %w", err)
	}
}

// collectSecrets gathers the values that must never appear in evidence.
func collectSecrets(cfg Config, harnesses *agentharness.Registry) []string {
	var secrets []string
	if name := cfg.Temporal.APIKeyEnv; name != "" {
		secrets = append(secrets, os.Getenv(name))
	}
	if cfg.Cube.APIKey != "" {
		secrets = append(secrets, cfg.Cube.APIKey)
	}
	// Named model resources may grant a credential independently of a
	// harness's static pass_env list. Register those values before any activity
	// can persist agent output.
	for _, model := range cfg.Models {
		if name := strings.TrimSpace(model.APIKeyEnv); name != "" {
			if value := os.Getenv(name); value != "" {
				secrets = append(secrets, value)
			}
		}
	}
	// Operator-declared credential environment variables are the values most
	// likely to be echoed by an agent.
	for _, hc := range cfg.Harnesses {
		if name := hc.APIKeyEnv; name != "" {
			if value := os.Getenv(name); value != "" {
				secrets = append(secrets, value)
			}
		}
		if path := strings.TrimSpace(hc.APIKeyFile); path != "" {
			if value, err := readCredentialFile(path); err == nil {
				secrets = append(secrets, value)
			}
		}
		for _, name := range hc.PassEnv {
			if value := os.Getenv(name); value != "" {
				secrets = append(secrets, value)
			}
		}
		// Provider session files are staged INTO the sandbox, so their contents
		// are exactly what an agent might echo back. They are read here only to
		// seed the redactor, and are never logged or persisted.
		for _, source := range hc.ProviderFiles {
			if data, err := os.ReadFile(source); err == nil {
				secrets = append(secrets, jsonLeafStrings(data)...)
			}
		}
	}
	_ = harnesses
	return secrets
}

// jsonLeafStrings returns every plausibly-secret string leaf of a JSON
// document, so those values can be scrubbed from captured evidence.
//
// It is deliberately conservative: only values of at least eight characters are
// returned, and the values are held in memory solely for redaction.
func jsonLeafStrings(data []byte) []string {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	var values []string
	var walk func(node any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			for _, v := range typed {
				walk(v)
			}
		case []any:
			for _, v := range typed {
				walk(v)
			}
		case string:
			if len(typed) >= 8 {
				values = append(values, typed)
			}
		}
	}
	walk(decoded)
	return values
}

func workerIdentity(prefix string) string {
	if prefix == "" {
		prefix = "factory-worker"
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown-host"
	}
	return fmt.Sprintf("%s@%s:%d", prefix, hostname, os.Getpid())
}
