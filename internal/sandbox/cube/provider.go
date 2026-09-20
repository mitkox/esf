package cube

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"

	"github.com/mitkox/esf/internal/sandbox"
)

// ProviderName is the identifier recorded in evidence.
const ProviderName = "cubesandbox"

// Provider is a sandbox.Provider backed by an existing CubeSandbox deployment.
type Provider struct {
	cfg    Config
	client *cubesandbox.Client
	log    *slog.Logger
}

// Option customizes a Provider.
type Option func(*Provider)

// WithLogger sets the structured logger.
func WithLogger(log *slog.Logger) Option {
	return func(p *Provider) {
		if log != nil {
			p.log = log
		}
	}
}

// New validates the configuration and returns a provider.
//
// The provider does not connect until first use; call Ping to verify
// reachability during startup.
func New(cfg Config, opts ...Option) (*Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	p := &Provider{
		cfg: cfg,
		log: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(p)
	}

	sdkCfg := cubesandbox.NewConfigFromEnv()
	sdkCfg.APIURL = cfg.APIURL
	sdkCfg.APIKey = cfg.APIKey
	sdkCfg.TemplateID = cfg.TemplateID
	sdkCfg.ProxyNodeIP = cfg.ProxyNodeIP
	sdkCfg.ProxyPortHTTP = cfg.ProxyPortHTTP
	if cfg.ProxyScheme != "" {
		sdkCfg.ProxyScheme = cfg.ProxyScheme
	}
	if cfg.SandboxDomain != "" {
		sdkCfg.SandboxDomain = cfg.SandboxDomain
	}
	if cfg.RequestTimeout > 0 {
		sdkCfg.RequestTimeout = cfg.RequestTimeout.Std()
	}
	p.client = cubesandbox.NewClient(sdkCfg)
	return p, nil
}

func (p *Provider) Name() string { return ProviderName }

// Capabilities reports the optional features this provider exposes.
//
// Snapshots and clones exist in the Cube SDK but are not wired into the factory
// yet, so they stay false until the factory implements and tests the Phase 2
// seams. Suspend, resume and preview ARE implemented here and are advertised,
// because the factory's review gate and operator tooling depend on knowing
// whether the capability is real.
func (p *Provider) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Snapshot:      false,
		Clone:         false,
		Rollback:      false,
		CloneMultiple: false,
		Suspend:       true,
		Resume:        true,
		Preview:       true,
	}
}

// Suspend checkpoints a sandbox so it can be resumed instead of rebuilt.
//
// The call waits for the sandbox to reach the paused state, because the
// factory's cost-control claim is only true once the checkpoint exists; a
// fire-and-forget pause would report success while the microVM is still live.
func (p *Provider) Suspend(ctx context.Context, sandboxID string) error {
	if strings.TrimSpace(sandboxID) == "" {
		return fmt.Errorf("%w: empty sandbox id", ErrInvalidSpec)
	}
	sb, err := p.client.Connect(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("connect to sandbox %s for suspend: %w", sandboxID, classify(err))
	}
	wait := true
	if err := sb.Pause(ctx, cubesandbox.PauseOptions{
		Wait:    &wait,
		Timeout: p.cfg.RequestTimeout.Std(),
	}); err != nil {
		return fmt.Errorf("suspend sandbox %s: %w", sandboxID, classify(err))
	}
	p.log.InfoContext(ctx, "cube sandbox suspended", slog.String("sandbox.id", sandboxID))
	return nil
}

// Resume brings a suspended sandbox back to running.
func (p *Provider) Resume(ctx context.Context, sandboxID string) error {
	if strings.TrimSpace(sandboxID) == "" {
		return fmt.Errorf("%w: empty sandbox id", ErrInvalidSpec)
	}
	sb, err := p.client.Connect(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("connect to sandbox %s for resume: %w", sandboxID, classify(err))
	}
	// Client.Connect auto-resumes a paused sandbox; an explicit resume call
	// makes the intent observable and covers deployments where Connect does not.
	//
	// The timeout is left to the server (nil): the factory's own wall-clock and
	// its verified cleanup are the real bounds on a run, and imposing the
	// configured idle timeout here would let a long run's sandbox expire
	// mid-flight after a review.
	if err := sb.Resume(ctx, nil); err != nil {
		return fmt.Errorf("resume sandbox %s: %w", sandboxID, classify(err))
	}
	p.log.InfoContext(ctx, "cube sandbox resumed", slog.String("sandbox.id", sandboxID))
	return nil
}

// PreviewURL returns the ingress URL for a port inside a sandbox.
//
// It refuses to invent an address: a deployment without a sandbox domain has no
// routable hostname, and returning a malformed URL would be worse than
// reporting that preview is unavailable.
func (p *Provider) PreviewURL(ctx context.Context, sandboxID string, port int) (string, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return "", fmt.Errorf("%w: empty sandbox id", ErrInvalidSpec)
	}
	if port <= 0 || port > 65535 {
		return "", fmt.Errorf("%w: port %d out of range", ErrInvalidSpec, port)
	}
	// Refuse to preview a checkpointed sandbox. The Cube SDK auto-resumes on
	// connect, which would make "show me the app" silently resume a sandbox the
	// operator had deliberately suspended. Waking a sandbox must be an explicit
	// decision, so the state is checked through the read-only list path first.
	state, err := p.SandboxState(ctx, sandboxID)
	if err != nil {
		return "", err
	}
	if state == sandbox.StatePaused {
		return "", fmt.Errorf("sandbox %s is suspended; resume it first (factory resume %s)", sandboxID, sandboxID)
	}
	sb, err := p.client.Connect(ctx, sandboxID)
	if err != nil {
		return "", fmt.Errorf("connect to sandbox %s for preview: %w", sandboxID, classify(err))
	}
	host := sb.GetHost(port)
	if !strings.Contains(host, ".") {
		return "", fmt.Errorf("%w: deployment has no sandbox domain; preview URLs are unavailable", ErrNotConfigured)
	}
	scheme := "http"
	if strings.EqualFold(p.cfg.ProxyScheme, "https") {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s", scheme, host), nil
}

// SandboxState reports a sandbox's lifecycle state without transitioning it.
//
// It reads through the list endpoint rather than connecting, because connecting
// to a suspended sandbox resumes it in the Cube SDK.
func (p *Provider) SandboxState(ctx context.Context, sandboxID string) (string, error) {
	infos, err := p.client.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list sandboxes: %w", classify(err))
	}
	for _, info := range infos {
		if info.SandboxID == sandboxID {
			return info.State, nil
		}
	}
	return "", fmt.Errorf("sandbox %s: %w", sandboxID, cubesandbox.ErrSandboxNotFound)
}

// Close releases idle HTTP connections. It does not destroy any sandbox.
func (p *Provider) Close() error { return p.client.Close() }

// Config returns a copy of the provider configuration.
func (p *Provider) Config() Config { return p.cfg }

// Ping verifies the Cube API is reachable and the configured template exists.
func (p *Provider) Ping(ctx context.Context) error {
	if _, err := p.client.Health(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if _, err := p.client.GetTemplate(ctx, p.cfg.TemplateID); err != nil {
		return fmt.Errorf("template %s: %w", p.cfg.TemplateID, classify(err))
	}
	return nil
}

// Create starts a sandbox from the configured template.
//
// Only operator-derived values are sent. The factory never requests a network
// policy that would weaken the deployment's defaults, so Spec.Network is
// intentionally ignored unless it sets something explicitly.
func (p *Provider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	templateID := spec.Template
	if templateID == "" {
		templateID = p.cfg.TemplateID
	}
	if templateID == "" {
		return nil, fmt.Errorf("%w: no template configured", ErrNotConfigured)
	}

	idle := spec.IdleTimeout
	if idle <= 0 {
		idle = p.cfg.IdleTimeout.Std()
	}

	opts := cubesandbox.CreateOptions{
		TemplateID: templateID,
		Timeout:    cubesandbox.DurationPtr(idle),
	}
	if len(spec.Metadata) > 0 {
		opts.Metadata = spec.Metadata
	}
	if len(spec.Env) > 0 {
		opts.EnvVars = spec.Env
	}
	// Network is only applied when the caller explicitly asked for something.
	// A zero-value Network leaves the deployment default untouched.
	if len(spec.Network.AllowOut) > 0 {
		opts.Network.AllowOut = spec.Network.AllowOut
	}
	if len(spec.Network.DenyOut) > 0 {
		opts.Network.DenyOut = spec.Network.DenyOut
	}
	if spec.Network.AllowInternet != nil {
		opts.AllowInternetAccess = spec.Network.AllowInternet
	}

	started := time.Now()
	created, err := p.client.Create(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("create sandbox from template %s: %w", templateID, classify(err))
	}
	p.log.InfoContext(ctx, "cube sandbox created",
		slog.String("sandbox.id", created.SandboxID),
		slog.String("sandbox.template", templateID),
		slog.Duration("duration", time.Since(started)))

	return &cubeSandbox{
		id:       created.SandboxID,
		template: templateID,
		sb:       created,
		log:      p.log,
	}, nil
}

// Reattach returns a handle to an existing sandbox so later activities in a
// durable workflow can keep using it.
func (p *Provider) Reattach(ctx context.Context, sandboxID string) (sandbox.Sandbox, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, fmt.Errorf("%w: empty sandbox id", ErrInvalidSpec)
	}
	sb, err := p.client.Connect(ctx, sandboxID)
	if err != nil {
		return nil, fmt.Errorf("reattach to sandbox %s: %w", sandboxID, classify(err))
	}
	return &cubeSandbox{
		id:       sb.SandboxID,
		template: sb.TemplateID,
		sb:       sb,
		log:      p.log,
	}, nil
}

// Destroy terminates a sandbox. It is idempotent: a sandbox that is already
// gone is reported as success, because cleanup must never mask the real
// outcome of a run.
func (p *Provider) Destroy(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return nil
	}
	sb, err := p.client.Connect(ctx, sandboxID)
	if err != nil {
		if errors.Is(err, cubesandbox.ErrSandboxNotFound) {
			p.log.InfoContext(ctx, "cube sandbox already gone", slog.String("sandbox.id", sandboxID))
			return nil
		}
		return fmt.Errorf("connect to sandbox %s for destroy: %w", sandboxID, classify(err))
	}
	if err := sb.Kill(ctx); err != nil {
		if errors.Is(err, cubesandbox.ErrSandboxNotFound) {
			return nil
		}
		return fmt.Errorf("destroy sandbox %s: %w", sandboxID, classify(err))
	}
	return nil
}

// List returns live sandboxes, used for leak detection and cleanup checks.
func (p *Provider) List(ctx context.Context) ([]sandbox.Info, error) {
	infos, err := p.client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sandboxes: %w", classify(err))
	}
	out := make([]sandbox.Info, 0, len(infos))
	for _, info := range infos {
		out = append(out, sandbox.Info{
			ID:         info.SandboxID,
			Template:   info.TemplateID,
			State:      info.State,
			StartedAt:  info.StartedAt,
			Metadata:   info.Metadata,
			ProviderID: ProviderName,
		})
	}
	return out, nil
}

// cubeSandbox adapts one CubeSandbox instance to sandbox.Sandbox.
type cubeSandbox struct {
	id       string
	template string
	sb       *cubesandbox.Sandbox
	log      *slog.Logger
}

func (s *cubeSandbox) ID() string       { return s.id }
func (s *cubeSandbox) Template() string { return s.template }

// Execute runs one command.
//
// Argv is rendered through sandbox.CommandLine, which single-quotes every
// element, so no argument can become shell syntax. This matters because the
// task text and repository contents are untrusted.
//
// A non-zero exit code is returned in the Execution, not as an error: a failing
// build or test is a *result* the factory must record, not a transport failure.
func (s *cubeSandbox) Execute(ctx context.Context, cmd sandbox.Command) (sandbox.Execution, error) {
	line, err := sandbox.CommandLine(cmd)
	if err != nil {
		return sandbox.Execution{}, err
	}

	opts := cubesandbox.CommandOptions{
		Cwd:     cmd.Dir,
		Timeout: cmd.Timeout,
	}
	if len(cmd.Env) > 0 {
		opts.Envs = cmd.Env
	}

	started := time.Now().UTC()
	result, err := s.sb.Commands().Run(ctx, line, opts)
	finished := time.Now().UTC()
	if err != nil {
		// Distinguish "the command could not run" from "the command ran and
		// failed". A context deadline means the sandbox is still fine.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return sandbox.Execution{
				ExitCode:    -1,
				StartedAt:   started,
				CompletedAt: finished,
				Duration:    finished.Sub(started),
				TimedOut:    true,
				Stderr:      "command timed out",
			}, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return sandbox.Execution{
				ExitCode:    -1,
				StartedAt:   started,
				CompletedAt: finished,
				Duration:    finished.Sub(started),
				Stderr:      "command cancelled",
			}, nil
		}
		return sandbox.Execution{}, fmt.Errorf("execute in sandbox %s: %w", s.id, classify(err))
	}

	return sandbox.Execution{
		ExitCode:    result.ExitCode,
		Stdout:      result.Stdout,
		Stderr:      result.Stderr,
		StartedAt:   started,
		CompletedAt: finished,
		Duration:    finished.Sub(started),
	}, nil
}

// WriteFile writes data into the sandbox, creating parent directories first.
func (s *cubeSandbox) WriteFile(ctx context.Context, path string, data []byte) error {
	if path == "" {
		return fmt.Errorf("write file: empty path")
	}
	// envd's files API does not create parent directories, so do it explicitly
	// with a structured argv (no shell interpretation of the path beyond the
	// provider's careful quoting).
	dir := parentDir(path)
	if dir != "" && dir != "/" {
		exec, err := s.Execute(ctx, sandbox.Command{
			Argv:        []string{"mkdir", "-p", dir},
			Description: "create parent directory",
		})
		if err != nil {
			return fmt.Errorf("create parent directory %s: %w", dir, err)
		}
		if !exec.Succeeded() {
			return fmt.Errorf("create parent directory %s: exit %d: %s", dir, exec.ExitCode, exec.Stderr)
		}
	}
	if err := s.sb.Files().Write(ctx, path, data); err != nil {
		return fmt.Errorf("write %s in sandbox %s: %w", path, s.id, classify(err))
	}
	return nil
}

// ReadFile reads a file from the sandbox.
func (s *cubeSandbox) ReadFile(ctx context.Context, path string) ([]byte, error) {
	content, err := s.sb.Files().Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read %s in sandbox %s: %w", path, s.id, classify(err))
	}
	return []byte(content), nil
}

// Destroy terminates this sandbox. It is idempotent.
func (s *cubeSandbox) Destroy(ctx context.Context) error {
	if err := s.sb.Kill(ctx); err != nil {
		if errors.Is(err, cubesandbox.ErrSandboxNotFound) {
			return nil
		}
		return fmt.Errorf("destroy sandbox %s: %w", s.id, classify(err))
	}
	return nil
}

// parentDir returns the directory portion of an absolute POSIX path.
func parentDir(path string) string {
	for i := len(path) - 1; i > 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}

// compile-time assertions that the adapter satisfies the factory interfaces.
var (
	_ sandbox.Provider = (*Provider)(nil)
	_ sandbox.Sandbox  = (*cubeSandbox)(nil)
)
