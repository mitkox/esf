package agentharness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

var testUnrealSHA256 = strings.Repeat("a", 64)

func TestUnrealHarnessUsesDirectJSONProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, err := NewUnreal(UnrealOptions{
		Binary:        "/host/unreal-agent-runner",
		BinarySHA256:  testUnrealSHA256,
		Provider:      "openrouter",
		BaseURL:       "https://openrouter.ai/api/v1",
		Model:         "vendor/model",
		APIKeyEnv:     "MODEL_API_KEY",
		ThinkingLevel: "xhigh",
		Timeout:       time.Minute,
		LookupEnv: func(name string) (string, bool) {
			if name == "MODEL_API_KEY" {
				return "super-secret", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatalf("NewUnreal: %v", err)
	}

	fake := sandbox.NewFake()
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	const prompt = `Change "hello"; rm -rf / && $(whoami)`
	result, err := h.Run(ctx, sb, Task{
		RunID: "run-123", Prompt: prompt,
		RepositoryDir: "/workspace/repository",
		Env: map[string]string{
			"UNRELATED_SECRET":        "must-not-be-replaced",
			unrealProviderEnvironment: "attacker-provider",
			unrealBaseURLEnvironment:  "http://attacker.invalid",
			unrealAPIKeyEnvironment:   "attacker-key",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(result.CommandLine, prompt) || strings.Contains(result.CommandLine, "python") || strings.Contains(result.CommandLine, "/bin/sh") {
		t.Fatalf("command line contains prompt or adapter shell: %q", result.CommandLine)
	}

	commands := fake.Commands(sb.ID())
	if len(commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(commands))
	}
	command := commands[0]
	wantArgv := []string{
		UnrealBinaryPath, "-workspace", ".",
		"-session-directory", UnrealSessionDirectory,
		"-log-directory", UnrealLogDirectory,
	}
	if strings.Join(command.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("argv = %v, want %v", command.Argv, wantArgv)
	}
	if command.Env[unrealProviderEnvironment] != "openrouter" || command.Env[unrealBaseURLEnvironment] != "https://openrouter.ai/api/v1" {
		t.Fatalf("provider environment = %v", command.Env)
	}
	if command.Env[unrealAPIKeyEnvironment] != "super-secret" {
		t.Fatal("operator credential was not mapped to the runner's canonical variable")
	}

	requestBytes, err := sb.ReadFile(ctx, command.StdinPath)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	var request unrealRequest
	if err := json.Unmarshal(requestBytes, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Model != "vendor/model" || request.ThinkingLevel != "xhigh" {
		t.Fatalf("request = %+v", request)
	}
	if len(request.Messages) != 1 || request.Messages[0].Content != prompt || request.Messages[0].MessageID == "" || request.SessionID == "" {
		t.Fatalf("request message/session identity is incomplete: %+v", request)
	}
	for _, id := range []string{request.SessionID, request.Messages[0].MessageID} {
		if strings.ContainsAny(id, "/_ .") {
			t.Fatalf("runner ID %q is not a safe localfile session identifier", id)
		}
	}
}

func TestUnrealHarnessUsesStableResumeIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, err := NewUnreal(UnrealOptions{
		Binary: "/host/runner", BinarySHA256: testUnrealSHA256, Provider: "ollama", BaseURL: "http://ollama.internal:11434/v1",
		Model: "model", Timeout: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewUnreal: %v", err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(ctx, sandbox.Spec{})
	task := Task{RunID: "same-run", Prompt: "same prompt", RepositoryDir: "/repo"}
	if _, err := h.Run(ctx, sb, task); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	commands := fake.Commands(sb.ID())
	first, _ := sb.ReadFile(ctx, commands[0].StdinPath)
	if _, err := h.Run(ctx, sb, task); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	commands = fake.Commands(sb.ID())
	if len(commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(commands))
	}
	second, _ := sb.ReadFile(ctx, commands[1].StdinPath)
	if string(first) != string(second) {
		t.Fatalf("retry request changed:\n%s\n%s", first, second)
	}
}

func TestUnrealHarnessRequiresCredentialBeforeExecution(t *testing.T) {
	t.Parallel()
	h, err := NewUnreal(UnrealOptions{
		Binary: "/host/runner", BinarySHA256: testUnrealSHA256, Provider: "openai", Model: "model",
		APIKeyEnv: "MISSING_KEY", Timeout: time.Minute,
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("NewUnreal: %v", err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(context.Background(), sandbox.Spec{})
	if _, err := h.Run(context.Background(), sb, Task{RunID: "run", Prompt: "task", RepositoryDir: "/repo"}); err == nil || !strings.Contains(err.Error(), "MISSING_KEY") {
		t.Fatalf("Run error = %v, want missing credential", err)
	}
	if got := len(fake.Commands(sb.ID())); got != 0 {
		t.Fatalf("executed %d commands without a credential", got)
	}
}

func TestUnrealHarnessUsesPlaceholderForEgressManagedCredential(t *testing.T) {
	t.Parallel()
	h, err := NewUnreal(UnrealOptions{
		Binary: "/host/runner", BinarySHA256: testUnrealSHA256,
		Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "model",
		EgressManaged: true, Timeout: time.Minute,
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("NewUnreal: %v", err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(context.Background(), sandbox.Spec{})
	if _, err := h.Run(context.Background(), sb, Task{RunID: "run", Prompt: "task", RepositoryDir: "/repo"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	commands := fake.Commands(sb.ID())
	if got := commands[0].Env[unrealAPIKeyEnvironment]; got != unrealEgressPlaceholder {
		t.Fatalf("API key placeholder = %q", got)
	}
}

func TestNewUnrealValidatesSecuritySensitiveOptions(t *testing.T) {
	t.Parallel()
	base := UnrealOptions{Binary: "/host/runner", BinarySHA256: testUnrealSHA256, Provider: "openrouter", Model: "model", APIKeyEnv: "API_KEY", Timeout: time.Minute}
	cases := []struct {
		name   string
		mutate func(*UnrealOptions)
	}{
		{"missing binary", func(o *UnrealOptions) { o.Binary = "" }},
		{"missing binary digest", func(o *UnrealOptions) { o.BinarySHA256 = "" }},
		{"invalid binary digest", func(o *UnrealOptions) { o.BinarySHA256 = "not-a-digest" }},
		{"unknown provider", func(o *UnrealOptions) { o.Provider = "other" }},
		{"missing model", func(o *UnrealOptions) { o.Model = "" }},
		{"model newline", func(o *UnrealOptions) { o.Model = "safe\ninjected" }},
		{"model whitespace", func(o *UnrealOptions) { o.Model = "model with spaces" }},
		{"URL userinfo", func(o *UnrealOptions) { o.BaseURL = "https://secret@example.com/v1" }},
		{"bad URL scheme", func(o *UnrealOptions) { o.BaseURL = "file:///tmp/socket" }},
		{"missing key name", func(o *UnrealOptions) { o.APIKeyEnv = "" }},
		{"invalid key name", func(o *UnrealOptions) { o.APIKeyEnv = "KEY;echo" }},
		{"invalid thinking", func(o *UnrealOptions) { o.ThinkingLevel = "extreme" }},
		{"egress requires https", func(o *UnrealOptions) { o.EgressManaged = true; o.BaseURL = "http://example.com/v1" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := base
			tc.mutate(&opts)
			if _, err := NewUnreal(opts); err == nil {
				t.Fatal("expected options to be rejected")
			}
		})
	}
}

func TestUnrealHarnessRejectsEmptyPromptAndInvalidModelOverride(t *testing.T) {
	t.Parallel()
	h, err := NewUnreal(UnrealOptions{Binary: "/host/runner", BinarySHA256: testUnrealSHA256, Provider: "ollama", Model: "model"})
	if err != nil {
		t.Fatalf("NewUnreal: %v", err)
	}
	fake := sandbox.NewFake()
	sb, _ := fake.Create(context.Background(), sandbox.Spec{})
	for _, task := range []Task{
		{RunID: "run", RepositoryDir: "/repo"},
		{RunID: "run", Prompt: "task", RepositoryDir: "/repo", Model: "bad model"},
		{RunID: "run", Prompt: "task", RepositoryDir: "/repo", Model: "other-model"},
	} {
		if _, err := h.Run(context.Background(), sb, task); err == nil {
			t.Fatalf("expected task %+v to be rejected", task)
		}
	}
	if got := len(fake.Commands(sb.ID())); got != 0 {
		t.Fatalf("executed %d commands for invalid tasks", got)
	}
}

func TestUnrealVersionUsesBinaryDigest(t *testing.T) {
	t.Parallel()
	h, err := NewUnreal(UnrealOptions{Binary: "/host/runner", BinarySHA256: testUnrealSHA256, Provider: "ollama", Model: "model"})
	if err != nil {
		t.Fatalf("NewUnreal: %v", err)
	}
	fake := sandbox.NewFake()
	fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		return sandbox.Execution{ExitCode: 0, Stdout: strings.Repeat("a", 64) + "  " + UnrealBinaryPath}, nil
	}
	sb, _ := fake.Create(context.Background(), sandbox.Spec{})
	if got := h.Version(context.Background(), sb); got != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("Version() = %q", got)
	}
}
