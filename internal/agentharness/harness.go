// Package agentharness defines how a coding agent is executed inside a sandbox.
//
// Principles this package enforces:
//
//   - Agents are replaceable. Workflow code depends on the Harness interface,
//     never on a specific agent CLI.
//   - An API caller selects a harness by NAME from an operator-controlled
//     registry. A caller can never supply an executable or an argument list.
//   - The harness owns the executable, the fixed argument vector, the timeout,
//     the environment allowlist, the prompt transport and output capture.
package agentharness

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// ModelArgsPlaceholder is substituted in Spec.Args with the model flag and
// model name.
//
// A placeholder (rather than a fixed position) is required because agent CLIs
// differ: `opencode2 run --model X` needs the flag AFTER the subcommand, while a
// simple `agent --model X` needs it before the prompt. A fixed insertion point
// cannot express both, and guessing produces a run that silently fails.
const ModelArgsPlaceholder = "{{model_args}}"

// PromptMode selects how the task text reaches the agent.
type PromptMode string

const (
	// PromptStdinFile writes the task to a file inside the sandbox and
	// redirects the agent's stdin from it. This is the default because the
	// untrusted text never appears in the command string at all.
	PromptStdinFile PromptMode = "stdin_file"
	// PromptArgv appends the task as a final argument. Safe because the sandbox
	// provider single-quotes every argument, but bounded by ARG_MAX.
	PromptArgv PromptMode = "argv"
)

// Task is one unit of work handed to an agent.
//
// Everything except Prompt and RepositoryDir is operator- or
// workflow-derived. Prompt is untrusted input.
type Task struct {
	// RunID is recorded in the sandbox and in evidence.
	RunID string
	// Prompt is the untrusted task text.
	Prompt string
	// PromptPath is where the prompt is written inside the sandbox when the
	// harness uses PromptStdinFile.
	PromptPath string
	// RepositoryDir is the working directory the agent runs in.
	RepositoryDir string
	// Model overrides the harness default model. Operator-configured allowed
	// models are enforced by the caller, not here.
	Model string
	// Timeout bounds the agent run.
	Timeout time.Duration
	// Env is operator-provisioned environment for the agent process.
	//
	// SECURITY: values here are visible to the agent. Only operator-approved
	// per-run credentials belong here, and they must never be derived from
	// untrusted task text.
	Env map[string]string
}

// ModelEndpoint is the non-secret part of a resolved model resource.
// Credential values never enter this structure; APIKeyEnv is only the name the
// harness configuration should reference.
type ModelEndpoint struct {
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

// ModelEndpointConfigurer is implemented by harnesses that can install a
// per-run endpoint configuration after their base provisioning completes.
// Generic harnesses need only the model ID and environment; OpenCode also
// needs its provider gateway written into opencode.json.
type ModelEndpointConfigurer interface {
	ConfigureModelEndpoint(ctx context.Context, sb sandbox.Sandbox, endpoint ModelEndpoint) error
}

// Result is the recorded outcome of an agent run.
//
// The optional cost/token fields exist so Phase 2 can compare harnesses and
// models. They are nil when the agent does not report them; the factory never
// depends on them.
type Result struct {
	Harness     string        `json:"harness"`
	Executable  string        `json:"executable"`
	CommandLine string        `json:"command_line"`
	Model       string        `json:"model,omitempty"`
	Version     string        `json:"version,omitempty"`
	ExitCode    int           `json:"exit_code"`
	Stdout      string        `json:"-"`
	Stderr      string        `json:"-"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at"`
	Duration    time.Duration `json:"duration"`
	TimedOut    bool          `json:"timed_out"`

	// Future-facing measurements; nil when unavailable.
	TokensIn  *int64   `json:"tokens_in,omitempty"`
	TokensOut *int64   `json:"tokens_out,omitempty"`
	CostUSD   *float64 `json:"cost_usd,omitempty"`
}

// Succeeded reports whether the agent exited zero and was not killed.
//
// Agent success does NOT imply factory success: verification decides that.
func (r Result) Succeeded() bool { return r.ExitCode == 0 && !r.TimedOut }

// Harness runs a coding agent inside a sandbox.
type Harness interface {
	// Name is the operator-facing identifier. Callers pass this, never a path.
	Name() string
	// Model is the harness's configured default model (may be empty).
	Model() string
	// Provision makes the agent available inside the sandbox. It is idempotent
	// and must not mutate anything outside the sandbox.
	Provision(ctx context.Context, sb sandbox.Sandbox) error
	// Run executes the agent. A non-zero agent exit code is a Result, not an
	// error; an error means the agent could not be run at all.
	Run(ctx context.Context, sb sandbox.Sandbox, task Task) (Result, error)
}

// Versioner is implemented by harnesses that can report the exact executable
// version used inside a sandbox. The factory records this value as evidence.
// Version discovery is best-effort and must never fail a run.
type Versioner interface {
	Version(ctx context.Context, sb sandbox.Sandbox) string
}

// ModelValidator lets a harness enforce operator model policy before the
// factory allocates a sandbox. Harnesses that do not implement it retain the
// generic factory behaviour.
type ModelValidator interface {
	ValidateModel(model string) error
}

// Registry maps operator-configured harness names to implementations.
//
// It is the ONLY way workflow code obtains a harness. There is deliberately no
// "arbitrary command" harness.
type Registry struct {
	harnesses map[string]Harness
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{harnesses: map[string]Harness{}}
}

// Register adds a harness. Names are lower-cased; duplicates are rejected
// rather than silently overwritten.
func (r *Registry) Register(h Harness) error {
	if h == nil {
		return fmt.Errorf("agentharness: nil harness")
	}
	name := strings.ToLower(strings.TrimSpace(h.Name()))
	if name == "" {
		return fmt.Errorf("agentharness: harness name is empty")
	}
	if _, exists := r.harnesses[name]; exists {
		return fmt.Errorf("agentharness: harness %q is already registered", name)
	}
	r.harnesses[name] = h
	return nil
}

// Resolve returns the harness registered under name.
//
// Resolution is by exact name only, so a caller cannot reach an arbitrary
// executable.
func (r *Registry) Resolve(name string) (Harness, error) {
	h, ok := r.harnesses[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, fmt.Errorf("agentharness: unknown harness %q (available: %s)", name, strings.Join(r.Names(), ", "))
	}
	return h, nil
}

// Has reports whether a harness name is registered.
func (r *Registry) Has(name string) bool {
	_, ok := r.harnesses[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// Names returns the registered harness names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.harnesses))
	for name := range r.harnesses {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FilterEnv returns only the entries whose keys appear in the allowlist.
//
// This is how the factory passes operator-approved credentials to an agent
// while guaranteeing that no other host environment variable (tokens, paths,
// proxy settings, CI variables) can leak into a sandbox.
func FilterEnv(env map[string]string, allowlist []string) map[string]string {
	if len(env) == 0 || len(allowlist) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		allowed[strings.TrimSpace(name)] = struct{}{}
	}
	out := map[string]string{}
	for name, value := range env {
		if _, ok := allowed[name]; ok && value != "" {
			out[name] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
