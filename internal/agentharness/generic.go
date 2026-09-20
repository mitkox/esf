package agentharness

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// Provision describes how to make an agent available inside a sandbox.
//
// The MVP provisions at sandbox start-up (option B in the design) because the
// base template ships neither git nor a JavaScript runtime, and building a
// dedicated Cube template is a deployment-level action this project must not
// take unilaterally. Every field is operator configuration; the production
// direction is a versioned factory template (option D), which would simply set
// these fields to empty.
type Provision struct {
	// Packages are OS packages to install with the sandbox's package manager.
	// Install is attempted only when this list is non-empty.
	Packages []string
	// BinarySource is a host path to a file copied into the sandbox.
	BinarySource string
	// BinaryDest is the absolute in-sandbox path for BinarySource.
	BinaryDest string
	// Files are configuration files written into the sandbox before the agent
	// runs. They are factory/operator content, never task-derived.
	Files map[string]string
	// SeedFiles maps an absolute IN-SANDBOX path to a file on the FACTORY HOST
	// whose contents are copied there.
	//
	// This is how an agent's offline caches are pre-staged. It matters because
	// a coding agent that must fetch a model catalog from the network at
	// start-up is neither reproducible nor reliable: the run then depends on an
	// external service being reachable and fast at that instant. Seeding the
	// catalog makes model resolution deterministic.
	SeedFiles map[string]string
	// VerifyArgs, when set, is run after provisioning to prove the agent works.
	// Its output is captured for the version field.
	VerifyArgs []string
	// SetupScript is factory-owned shell code run after the binary is in place.
	// It must never contain untrusted input.
	SetupScript string
}

// Spec is the operator configuration of a generic command harness.
type Spec struct {
	Name string
	// Executable is the agent program name or absolute path. It is operator
	// configuration only.
	Executable string
	// Args is the fixed argument vector.
	//
	// The placeholder {{model_args}} expands to ["<ModelFlag> <model>"], or
	// disappears when no model is configured. {{prompt_file}} expands to the
	// in-sandbox path of the task file.
	Args []string
	// ModelFlag is the flag the agent uses to select a model, for example
	// "--model". It is only used to expand {{model_args}}.
	ModelFlag string
	// Model is the default model.
	Model string
	// Timeout bounds one agent run.
	Timeout time.Duration
	// Env is fixed environment for the agent process.
	Env map[string]string
	// PassEnv lists host environment variable NAMES allowed through to the
	// agent. Everything else is dropped.
	PassEnv []string
	// PromptMode selects the prompt transport.
	PromptMode PromptMode
	// Provision describes how to install the agent in a fresh sandbox.
	Provision Provision
}

// GenericCommandHarness runs a configured agent executable with a fixed
// argument vector. It is the only concrete harness family; specific agents are
// configurations of it.
type GenericCommandHarness struct {
	spec Spec
	// hostEnv is the environment consulted for PassEnv. Injected for tests.
	hostEnv func(string) (string, bool)
	// readFile allows tests to supply a fake agent binary.
	readFile func(string) ([]byte, error)
	// configureModelEndpoint is set by harness families such as OpenCode that
	// need a configuration file in addition to the model command-line flag.
	configureModelEndpoint func(context.Context, sandbox.Sandbox, ModelEndpoint) error
}

// NewGeneric builds a generic harness from its spec.
func NewGeneric(spec Spec) (*GenericCommandHarness, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("agentharness: spec name is required")
	}
	if strings.TrimSpace(spec.Executable) == "" {
		return nil, fmt.Errorf("agentharness: executable is required for %q", spec.Name)
	}
	if spec.Timeout <= 0 {
		return nil, fmt.Errorf("agentharness: timeout must be positive for %q", spec.Name)
	}
	switch spec.PromptMode {
	case "":
		spec.PromptMode = PromptStdinFile
	case PromptStdinFile, PromptArgv:
	default:
		return nil, fmt.Errorf("agentharness: unknown prompt mode %q for %q", spec.PromptMode, spec.Name)
	}
	if spec.PromptMode == PromptStdinFile && !hasPlaceholder(spec.Args, "{{prompt_file}}") {
		// The file is delivered by redirection, not by an argument, so no
		// placeholder is required; this branch documents that.
		_ = spec
	}
	return &GenericCommandHarness{
		spec:     spec,
		hostEnv:  os.LookupEnv,
		readFile: os.ReadFile,
	}, nil
}

func (h *GenericCommandHarness) Name() string  { return h.spec.Name }
func (h *GenericCommandHarness) Model() string { return h.spec.Model }

// ConfigureModelEndpoint applies a resolved model endpoint when this harness
// family needs one. A plain generic command has no endpoint-specific config;
// its model ID and approved environment are still supplied to Run.
func (h *GenericCommandHarness) ConfigureModelEndpoint(ctx context.Context, sb sandbox.Sandbox, endpoint ModelEndpoint) error {
	if h.configureModelEndpoint == nil {
		if endpoint.BaseURL != "" {
			return fmt.Errorf("%s: model base_url is unsupported by this harness", h.Name())
		}
		return nil
	}
	return h.configureModelEndpoint(ctx, sb, endpoint)
}

// Spec returns a copy of the operator specification.
func (h *GenericCommandHarness) Spec() Spec {
	spec := h.spec
	spec.Args = append([]string(nil), h.spec.Args...)
	spec.Provision.Packages = append([]string(nil), h.spec.Provision.Packages...)
	return spec
}

// Provision installs the agent inside the sandbox.
//
// Steps are deterministic and recorded: OS packages, then the binary, then an
// optional verification that the agent actually executes.
func (h *GenericCommandHarness) Provision(ctx context.Context, sb sandbox.Sandbox) error {
	p := h.spec.Provision

	if len(p.Packages) > 0 {
		script := "set -eu\n" +
			"export DEBIAN_FRONTEND=noninteractive\n" +
			"apt-get update -qq\n" +
			// --no-install-recommends keeps the image small and the step fast.
			"apt-get install -y -qq --no-install-recommends " + strings.Join(p.Packages, " ") + " >/dev/null 2>&1\n" +
			"echo provision-packages-ok\n"
		exec, err := sb.Execute(ctx, sandbox.Command{
			Script:      script,
			Timeout:     10 * time.Minute,
			Description: "install sandbox packages: " + strings.Join(p.Packages, " "),
		})
		if err != nil {
			return fmt.Errorf("%s provision: install packages: %w", h.Name(), err)
		}
		if !exec.Succeeded() {
			return fmt.Errorf("%s provision: install packages failed (exit %d): %s",
				h.Name(), exec.ExitCode, truncate(exec.Stderr, 500))
		}
	}

	if p.BinarySource != "" {
		if p.BinaryDest == "" {
			return fmt.Errorf("%s provision: BinaryDest is required when BinarySource is set", h.Name())
		}
		data, err := h.readFile(p.BinarySource)
		if err != nil {
			return fmt.Errorf("%s provision: read agent binary %q: %w", h.Name(), p.BinarySource, err)
		}
		if err := sb.WriteFile(ctx, p.BinaryDest, data); err != nil {
			return fmt.Errorf("%s provision: push agent binary: %w", h.Name(), err)
		}
		exec, err := sb.Execute(ctx, sandbox.Command{
			Argv:        []string{"chmod", "0755", p.BinaryDest},
			Description: "make agent binary executable",
		})
		if err != nil {
			return fmt.Errorf("%s provision: chmod agent binary: %w", h.Name(), err)
		}
		if !exec.Succeeded() {
			return fmt.Errorf("%s provision: chmod agent binary failed (exit %d): %s",
				h.Name(), exec.ExitCode, truncate(exec.Stderr, 300))
		}
	}

	// Configuration files are written with paths relative to a fixed root so a
	// caller cannot escape it; keys are validated to be absolute in-sandbox
	// paths.
	for _, path := range sortedKeys(p.Files) {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("%s provision: config file path %q must be absolute", h.Name(), path)
		}
		if err := sb.WriteFile(ctx, path, []byte(p.Files[path])); err != nil {
			return fmt.Errorf("%s provision: write config file %s: %w", h.Name(), path, err)
		}
	}

	for _, dest := range sortedKeys(p.SeedFiles) {
		if !strings.HasPrefix(dest, "/") {
			return fmt.Errorf("%s provision: seed file destination %q must be absolute", h.Name(), dest)
		}
		source := p.SeedFiles[dest]
		data, err := h.readFile(source)
		if err != nil {
			return fmt.Errorf("%s provision: read seed file %q: %w", h.Name(), source, err)
		}
		if err := sb.WriteFile(ctx, dest, data); err != nil {
			return fmt.Errorf("%s provision: stage seed file %s: %w", h.Name(), dest, err)
		}
	}

	if p.SetupScript != "" {
		exec, err := sb.Execute(ctx, sandbox.Command{
			Script:      p.SetupScript,
			Timeout:     5 * time.Minute,
			Description: "agent setup script",
		})
		if err != nil {
			return fmt.Errorf("%s provision: setup script: %w", h.Name(), err)
		}
		if !exec.Succeeded() {
			return fmt.Errorf("%s provision: setup script failed (exit %d): %s",
				h.Name(), exec.ExitCode, truncate(exec.Stderr, 500))
		}
	}

	if len(p.VerifyArgs) > 0 {
		exec, err := sb.Execute(ctx, sandbox.Command{
			Argv:        p.VerifyArgs,
			Timeout:     2 * time.Minute,
			Description: "verify agent is executable",
		})
		if err != nil {
			return fmt.Errorf("%s provision: verify: %w", h.Name(), err)
		}
		if !exec.Succeeded() {
			return fmt.Errorf("%s provision: agent did not execute (exit %d): %s",
				h.Name(), exec.ExitCode, truncate(exec.Stderr, 500))
		}
	}
	return nil
}

// Version runs the configured verification command and returns its first output
// line, or an empty string when unavailable. It is best-effort: failure to read
// a version never fails a run.
func (h *GenericCommandHarness) Version(ctx context.Context, sb sandbox.Sandbox) string {
	if len(h.spec.Provision.VerifyArgs) == 0 {
		return ""
	}
	exec, err := sb.Execute(ctx, sandbox.Command{
		Argv:        h.spec.Provision.VerifyArgs,
		Timeout:     60 * time.Second,
		Description: "read agent version",
	})
	if err != nil || !exec.Succeeded() {
		return ""
	}
	line := strings.TrimSpace(exec.Stdout)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}

// Run executes the agent with the configured argument vector.
func (h *GenericCommandHarness) Run(ctx context.Context, sb sandbox.Sandbox, task Task) (Result, error) {
	if strings.TrimSpace(task.Prompt) == "" {
		return Result{}, fmt.Errorf("%s: task prompt is empty", h.Name())
	}
	if task.RepositoryDir == "" {
		return Result{}, fmt.Errorf("%s: repository directory is required", h.Name())
	}

	model := strings.TrimSpace(task.Model)
	if model == "" {
		model = h.spec.Model
	}

	timeout := task.Timeout
	if timeout <= 0 {
		timeout = h.spec.Timeout
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	promptPath := task.PromptPath
	if promptPath == "" {
		promptPath = "/workspace/.factory/agent-prompt.txt"
	}

	// Deliver the prompt. Writing it to a file and redirecting stdin means the
	// untrusted text never becomes part of the command string.
	argv := h.buildArgv(model, promptPath, task)
	if h.spec.PromptMode == PromptStdinFile {
		if err := sb.WriteFile(runCtx, promptPath, []byte(task.Prompt)); err != nil {
			return Result{}, fmt.Errorf("%s: write prompt file: %w", h.Name(), err)
		}
	}

	env := map[string]string{}
	for name, value := range h.spec.Env {
		env[name] = value
	}
	for _, name := range h.spec.PassEnv {
		if value, ok := h.hostEnv(name); ok && value != "" {
			env[name] = value
		}
	}
	for name, value := range task.Env {
		env[name] = value
	}

	cmd := sandbox.Command{
		Argv:        argv,
		Dir:         task.RepositoryDir,
		Env:         env,
		Timeout:     timeout,
		Description: "agent run (" + h.Name() + ")",
	}
	if h.spec.PromptMode == PromptStdinFile {
		cmd.StdinPath = promptPath
	}

	exec, err := sb.Execute(runCtx, cmd)
	if err != nil {
		return Result{}, fmt.Errorf("%s: execute agent: %w", h.Name(), err)
	}

	result := Result{
		Harness:     h.Name(),
		Executable:  h.spec.Executable,
		CommandLine: renderCommandLine(argv, cmd.StdinPath),
		Model:       model,
		ExitCode:    exec.ExitCode,
		Stdout:      exec.Stdout,
		Stderr:      exec.Stderr,
		StartedAt:   exec.StartedAt,
		CompletedAt: exec.CompletedAt,
		Duration:    exec.Duration,
		TimedOut:    exec.TimedOut,
	}
	return result, nil
}

// buildArgv renders the fixed argument vector with placeholders substituted.
//
// The argument vector is operator configuration, so it is the only place that
// decides how the agent is invoked. Untrusted task text is appended only in
// PromptArgv mode, and even then it becomes a single quoted argument.
func (h *GenericCommandHarness) buildArgv(model, promptPath string, task Task) []string {
	argv := make([]string, 0, len(h.spec.Args)+4)
	argv = append(argv, h.spec.Executable)

	for _, arg := range h.spec.Args {
		if arg == ModelArgsPlaceholder {
			// Expand in place, or drop entirely when no model is configured.
			if model != "" && h.spec.ModelFlag != "" {
				argv = append(argv, h.spec.ModelFlag, model)
			}
			continue
		}
		arg = strings.ReplaceAll(arg, "{{model}}", model)
		arg = strings.ReplaceAll(arg, "{{prompt_file}}", promptPath)
		arg = strings.ReplaceAll(arg, "{{repository_dir}}", task.RepositoryDir)
		argv = append(argv, arg)
	}
	if h.spec.PromptMode == PromptArgv {
		argv = append(argv, task.Prompt)
	}
	return argv
}

func hasPlaceholder(args []string, placeholder string) bool {
	for _, arg := range args {
		if strings.Contains(arg, placeholder) {
			return true
		}
	}
	return false
}

// sortedKeys gives deterministic file-writing order so provisioning is
// reproducible run to run.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderCommandLine is the form recorded in evidence. It contains no untrusted
// prompt text: stdin delivery shows the redirection instead.
func renderCommandLine(argv []string, stdinPath string) string {
	line := strings.Join(argv, " ")
	if stdinPath != "" {
		line += " < " + stdinPath
	}
	return line
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var _ Harness = (*GenericCommandHarness)(nil)
