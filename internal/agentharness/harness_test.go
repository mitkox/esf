package agentharness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

type stubHarness struct {
	name string
}

func (s stubHarness) Name() string { return s.name }
func (s stubHarness) Model() string {
	return "stub-model"
}
func (s stubHarness) Provision(context.Context, sandbox.Sandbox) error { return nil }
func (s stubHarness) Run(context.Context, sandbox.Sandbox, Task) (Result, error) {
	return Result{}, nil
}

func TestRegistryResolvesByNameOnly(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	if err := registry.Register(stubHarness{name: "opencode2"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(stubHarness{name: "opencode2"}); err == nil {
		t.Fatal("expected a duplicate registration to fail")
	}

	if _, err := registry.Resolve("opencode2"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// An unknown name must not fall back to anything: choosing an executable is
	// operator policy, never caller input.
	if _, err := registry.Resolve("/bin/sh"); err == nil {
		t.Fatal("expected a path-like name to be rejected")
	}
	if _, err := registry.Resolve("opencode"); err == nil {
		t.Fatal("expected an unregistered name to be rejected")
	}
	if got := registry.Names(); len(got) != 1 || got[0] != "opencode2" {
		t.Fatalf("Names() = %v, want [opencode2]", got)
	}
}

func TestFilterEnvAllowsOnlyListedNames(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"AWS_SECRET_ACCESS_KEY": "leak-me",
		"PATH":                  "/usr/bin",
		"FACTORY_AGENT_TOKEN":   "allowed",
	}
	filtered := FilterEnv(env, []string{"FACTORY_AGENT_TOKEN"})
	if len(filtered) != 1 {
		t.Fatalf("FilterEnv kept %d entries, want 1: %v", len(filtered), filtered)
	}
	if filtered["FACTORY_AGENT_TOKEN"] != "allowed" {
		t.Fatalf("allowed variable was dropped: %v", filtered)
	}
	// With no allowlist nothing passes: the safe default is to leak nothing.
	if got := FilterEnv(env, nil); got != nil {
		t.Fatalf("FilterEnv with an empty allowlist returned %v, want nil", got)
	}
}

func TestGenericHarnessDeliversPromptViaStdinFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	h, err := NewGeneric(Spec{
		Name:       "test-agent",
		Executable: "agent",
		Args:       []string{"run"},
		Timeout:    time.Minute,
		PromptMode: PromptStdinFile,
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}

	fake := sandbox.NewFake()
	sb, err := fake.Create(ctx, sandbox.Spec{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const prompt = "do the thing; rm -rf / && $(whoami)"
	result, err := h.Run(ctx, sb, Task{
		Prompt:        prompt,
		PromptPath:    "/workspace/.factory/prompt.txt",
		RepositoryDir: "/workspace/repo",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The prompt must be written to a file...
	written, err := sb.ReadFile(ctx, "/workspace/.factory/prompt.txt")
	if err != nil {
		t.Fatalf("prompt file was not written: %v", err)
	}
	if string(written) != prompt {
		t.Fatalf("prompt file = %q, want %q", written, prompt)
	}

	// ...and must NOT appear in the command string.
	if strings.Contains(result.CommandLine, "rm -rf") {
		t.Fatalf("untrusted prompt text leaked into the command line: %q", result.CommandLine)
	}
	if !strings.Contains(result.CommandLine, "prompt.txt") {
		t.Fatalf("command line should show the stdin redirection: %q", result.CommandLine)
	}

	// The provider must have received the redirection, not the text.
	cmds := fake.Commands(sb.ID())
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
	if cmds[0].StdinPath != "/workspace/.factory/prompt.txt" {
		t.Fatalf("StdinPath = %q", cmds[0].StdinPath)
	}
	if cmds[0].Dir != "/workspace/repo" {
		t.Fatalf("Dir = %q, want the repository directory", cmds[0].Dir)
	}
}

func TestGenericHarnessModelPlacement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	h, err := NewGeneric(Spec{
		Name:       "subcommand-agent",
		Executable: "agent",
		// The placeholder must be expanded where the CLI expects it: after the
		// subcommand, not before it.
		Args:      []string{"run", ModelArgsPlaceholder, "--json"},
		ModelFlag: "--model",
		Model:     "vendor/model-a",
		Timeout:   time.Minute,
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}

	fake := sandbox.NewFake()
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	if _, err := h.Run(ctx, sb, Task{Prompt: "x", RepositoryDir: "/repo"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	line := fake.Commands(sb.ID())[0]
	want := []string{"agent", "run", "--model", "vendor/model-a", "--json"}
	if len(line.Argv) != len(want) {
		t.Fatalf("argv = %v, want %v", line.Argv, want)
	}
	for i := range want {
		if line.Argv[i] != want[i] {
			t.Fatalf("argv = %v, want %v", line.Argv, want)
		}
	}
}

func TestGenericHarnessOmitsModelFlagWhenUnset(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	h, err := NewGeneric(Spec{
		Name:       "agent",
		Executable: "agent",
		Args:       []string{"run", ModelArgsPlaceholder},
		ModelFlag:  "--model",
		Timeout:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	if _, err := h.Run(ctx, sb, Task{Prompt: "x", RepositoryDir: "/repo"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fake.Commands(sb.ID())[0].Argv; len(got) != 2 {
		t.Fatalf("argv = %v, want the model flag to be dropped entirely", got)
	}
}

func TestGenericHarnessRejectsEmptyPrompt(t *testing.T) {
	t.Parallel()
	h, err := NewGeneric(Spec{Name: "a", Executable: "a", Timeout: time.Minute})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(context.Background(), sandbox.Spec{})
	if _, err := h.Run(context.Background(), sb, Task{RepositoryDir: "/repo"}); err == nil {
		t.Fatal("expected an error for an empty prompt")
	}
}

func TestGenericHarnessProvisionInstallsPackagesAndBinary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	h, err := NewGeneric(Spec{
		Name:       "provisioned",
		Executable: "agent",
		Timeout:    time.Minute,
		Provision: Provision{
			Packages:     []string{"git"},
			BinarySource: "/host/agent",
			BinaryDest:   "/usr/local/bin/agent",
			Files:        map[string]string{"/root/.config/agent.json": `{"k":"v"}`},
			SeedFiles:    map[string]string{"/root/.cache/catalog.json": "/host/catalog.json"},
			VerifyArgs:   []string{"agent", "--version"},
		},
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	h.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/host/agent":
			return []byte("binary"), nil
		case "/host/catalog.json":
			return []byte("catalog"), nil
		default:
			return nil, errors.New("unexpected path " + path)
		}
	}

	fake := sandbox.NewFake()
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	if err := h.Provision(ctx, sb); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	if data, err := sb.ReadFile(ctx, "/usr/local/bin/agent"); err != nil || string(data) != "binary" {
		t.Fatalf("agent binary was not pushed: %q %v", data, err)
	}
	if data, err := sb.ReadFile(ctx, "/root/.config/agent.json"); err != nil || string(data) != `{"k":"v"}` {
		t.Fatalf("config file was not written: %q %v", data, err)
	}
	if data, err := sb.ReadFile(ctx, "/root/.cache/catalog.json"); err != nil || string(data) != "catalog" {
		t.Fatalf("seed file was not staged: %q %v", data, err)
	}
	if !fake.ContainsCommand(sb.ID(), "apt-get install") {
		t.Fatal("package installation was not attempted")
	}
	if !fake.ContainsCommand(sb.ID(), "chmod") {
		t.Fatal("the agent binary was not made executable")
	}
}

func TestGenericHarnessRejectsBinaryDigestMismatchBeforeStaging(t *testing.T) {
	t.Parallel()
	h, err := NewGeneric(Spec{
		Name: "pinned", Executable: "agent", Timeout: time.Minute,
		Provision: Provision{
			BinarySource: "/host/agent", BinaryDest: "/usr/local/bin/agent",
			BinarySHA256: strings.Repeat("a", 64),
		},
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	h.readFile = func(string) ([]byte, error) { return []byte("different binary"), nil }
	fake := sandbox.NewFake()
	sb, _ := fake.Create(context.Background(), sandbox.Spec{})
	if err := h.Provision(context.Background(), sb); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("Provision error = %v, want digest mismatch", err)
	}
	if _, err := sb.ReadFile(context.Background(), "/usr/local/bin/agent"); err == nil {
		t.Fatal("mismatched binary was staged")
	}
}

func TestGenericHarnessProvisionRejectsRelativeSeedPath(t *testing.T) {
	t.Parallel()
	h, err := NewGeneric(Spec{
		Name:       "p",
		Executable: "a",
		Timeout:    time.Minute,
		Provision:  Provision{SeedFiles: map[string]string{"relative.json": "/host/x"}},
	})
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(context.Background(), sandbox.Spec{})
	if err := h.Provision(context.Background(), sb); err == nil {
		t.Fatal("expected a relative seed destination to be rejected")
	}
}

func TestNewGenericValidatesSpec(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		spec Spec
	}{
		{"missing name", Spec{Executable: "a", Timeout: time.Minute}},
		{"missing executable", Spec{Name: "a", Timeout: time.Minute}},
		{"non-positive timeout", Spec{Name: "a", Executable: "a"}},
		{"unknown prompt mode", Spec{Name: "a", Executable: "a", Timeout: time.Minute, PromptMode: "telepathy"}},
		{"unsafe name", Spec{Name: "../agent", Executable: "a", Timeout: time.Minute}},
		{"unsafe package", Spec{Name: "a", Executable: "a", Timeout: time.Minute, Provision: Provision{Packages: []string{"git;id"}}}},
		{"invalid pass env", Spec{Name: "a", Executable: "a", Timeout: time.Minute, PassEnv: []string{"KEY;id"}}},
		{"embedded model placeholder", Spec{Name: "a", Executable: "/bin/sh", Args: []string{"-c", "run {{model}}"}, Timeout: time.Minute}},
		{"embedded repository placeholder", Spec{Name: "a", Executable: "/bin/sh", Args: []string{"-c", "cd {{repository_dir}}"}, Timeout: time.Minute}},
		{"shell program model placeholder", Spec{Name: "a", Executable: "/bin/sh", Args: []string{"-c", "{{model}}"}, Timeout: time.Minute}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewGeneric(tc.spec); err == nil {
				t.Fatal("expected the spec to be rejected")
			}
		})
	}
}

func TestOpenCodeHarnessDefaults(t *testing.T) {
	t.Parallel()
	h, err := NewOpenCode(OpenCodeOptions{
		Binary:       "/host/opencode",
		Model:        "vendor/model",
		ReadFile:     func(string) ([]byte, error) { return []byte("bin"), nil },
		CatalogCache: "/host/models.json",
	})
	if err != nil {
		t.Fatalf("NewOpenCode: %v", err)
	}
	if h.Name() != "opencode2" {
		t.Fatalf("Name() = %q, want opencode2", h.Name())
	}
	if h.Model() != "vendor/model" {
		t.Fatalf("Model() = %q", h.Model())
	}

	spec := h.Spec()
	// The model flag must land after the subcommand.
	if len(spec.Args) < 2 || spec.Args[0] != "run" || spec.Args[1] != ModelArgsPlaceholder {
		t.Fatalf("Args = %v, want the model placeholder after the subcommand", spec.Args)
	}
	if spec.Provision.BinaryDest != "/usr/local/bin/opencode2" {
		t.Fatalf("BinaryDest = %q", spec.Provision.BinaryDest)
	}
	if _, ok := spec.Provision.Files[OpenCodeConfigPath]; !ok {
		t.Fatal("the agent configuration file was not provisioned")
	}
	if _, ok := spec.Provision.SeedFiles[OpenCodeCatalogCachePath]; !ok {
		t.Fatal("the model catalog was not seeded")
	}
}

func TestOpenCodeConfigDoesNotEmbedCredentialValue(t *testing.T) {
	t.Parallel()
	config, err := defaultOpenCodeConfig("http://gateway.example/v1", "FACTORY_MODEL_KEY", "factory/mitko")
	if err != nil {
		t.Fatalf("defaultOpenCodeConfig: %v", err)
	}
	encoded := strings.TrimSpace(string(mustJSON(t, config)))
	// The credential must be referenced by name, never inlined.
	if !strings.Contains(encoded, "{env:FACTORY_MODEL_KEY}") {
		t.Fatalf("expected an env-var reference in the provider config: %s", encoded)
	}
	if !strings.Contains(encoded, "@opencode/ai/providers/openai-compatible") {
		t.Fatalf("expected the V2 openai-compatible provider package: %s", encoded)
	}
	if !strings.Contains(encoded, `"model":"factory/mitko"`) || !strings.Contains(encoded, `"modelID":"mitko"`) {
		t.Fatalf("expected the local model to be registered: %s", encoded)
	}
}

func TestOpenCodeConfiguresResolvedModelEndpoint(t *testing.T) {
	h, err := NewOpenCode(OpenCodeOptions{
		Binary:   "/host/opencode",
		ReadFile: func(string) ([]byte, error) { return []byte("bin"), nil },
	})
	if err != nil {
		t.Fatalf("NewOpenCode: %v", err)
	}
	provider := sandbox.NewFake()
	sb, err := provider.Create(context.Background(), sandbox.Spec{Template: "test"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	endpoint := ModelEndpoint{
		Provider: "test", Model: "provider/model", BaseURL: "https://gateway.example/v1", APIKeyEnv: "FACTORY_MODEL_KEY",
	}
	if err := h.ConfigureModelEndpoint(context.Background(), sb, endpoint); err != nil {
		t.Fatalf("ConfigureModelEndpoint: %v", err)
	}
	data, err := sb.ReadFile(context.Background(), OpenCodeConfigPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, endpoint.BaseURL) || !strings.Contains(text, "{env:"+endpoint.APIKeyEnv+"}") {
		t.Fatalf("resolved endpoint missing from opencode config: %s", text)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
