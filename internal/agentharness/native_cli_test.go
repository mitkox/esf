package agentharness

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

func TestNativeCLIPromptAndCredentialIsolation(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			h, err := newNativeCLI(kind, NativeCLIOptions{EgressManaged: true})
			if err != nil {
				t.Fatal(err)
			}
			h.lookupEnv = func(string) (string, bool) { t.Fatal("read host credential in managed mode"); return "", false }
			fake := sandbox.NewFake()
			fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
				if slices.Contains(cmd.Argv, "--version") {
					return sandbox.Execution{}, nil
				}
				keyEnv := "ANTHROPIC_API_KEY"
				terminal := `{"type":"result","subtype":"success","is_error":false}`
				if kind == "codex" {
					keyEnv = "ESF_CODEX_API_KEY"
					terminal = `{"type":"turn.completed"}`
				}
				if cmd.Env[keyEnv] != "cube-egress-managed-placeholder" {
					t.Fatalf("key = %q", cmd.Env[keyEnv])
				}
				if cmd.StdinPath == "" || cmd.Dir != "/repo" {
					t.Fatalf("prompt delivery = %+v", cmd)
				}
				return sandbox.Execution{Stdout: terminal}, nil
			}
			sb, _ := fake.Create(ctx, sandbox.Spec{})
			// Seed the endpoint without staging an actual CLI in this unit test.
			if err := h.ConfigureModelEndpoint(ctx, sb, ModelEndpoint{}); err != nil {
				t.Fatal(err)
			}
			prompt := "-p injected; $(touch /bad)\nfix the code"
			result, err := h.Run(ctx, sb, Task{Prompt: prompt, RepositoryDir: "/repo", Model: "operator-model", Env: map[string]string{"ANTHROPIC_API_KEY": "do-not-use"}})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if strings.Contains(result.CommandLine, prompt) {
				t.Fatal("prompt entered command line")
			}
			commands := fake.Commands(sb.ID())
			cmd := commands[len(commands)-1]
			written, _ := sb.ReadFile(ctx, cmd.StdinPath)
			if string(written) != prompt {
				t.Fatal("prompt changed")
			}
			if !slices.Contains(cmd.Argv, "operator-model") {
				t.Fatal("model override lost")
			}
		})
	}
}

func TestNativeCLIResolvedEndpointsArePerSandbox(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			h, err := newNativeCLI(kind, NativeCLIOptions{})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			fake := sandbox.NewFake()
			first, _ := fake.Create(ctx, sandbox.Spec{})
			second, _ := fake.Create(ctx, sandbox.Spec{})
			for _, item := range []struct {
				sb      sandbox.Sandbox
				baseURL string
			}{{first, "https://first.example/v1"}, {second, "https://second.example/v1"}} {
				if err := h.ConfigureModelEndpoint(ctx, item.sb, ModelEndpoint{BaseURL: item.baseURL, APIKeyEnv: "RESOLVED_KEY"}); err != nil {
					t.Fatal(err)
				}
				data, _ := item.sb.ReadFile(ctx, h.endpointPath())
				var endpoint ModelEndpoint
				if err := json.Unmarshal(data, &endpoint); err != nil {
					t.Fatal(err)
				}
				if endpoint.BaseURL != item.baseURL {
					t.Fatal("endpoint shared between runs")
				}
			}
			h.lookupEnv = func(name string) (string, bool) {
				if name != "RESOLVED_KEY" {
					t.Fatalf("lookup %s", name)
				}
				return "secret", true
			}
			fake.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
				if kind == "claude" && (cmd.Env["ANTHROPIC_BASE_URL"] != "https://first.example/v1" || cmd.Env["ANTHROPIC_API_KEY"] != "secret") {
					t.Fatal("Claude resolved endpoint not applied")
				}
				if kind == "codex" && (!slices.Contains(cmd.Argv, `model_providers.esf.base_url="https://first.example/v1"`) || cmd.Env["ESF_CODEX_API_KEY"] != "secret") {
					t.Fatal("Codex resolved endpoint not applied")
				}
				return sandbox.Execution{ExitCode: 7}, nil
			}
			result, err := h.Run(ctx, first, Task{Prompt: "fix", RepositoryDir: "/repo"})
			if err != nil || result.ExitCode != 7 {
				t.Fatal("CLI failure lost")
			}
			if strings.Contains(result.CommandLine, "secret") {
				t.Fatal("credential recorded in command line")
			}
		})
	}
}

func TestNativeCLITerminalFailure(t *testing.T) {
	for _, item := range []struct {
		kind, output string
		success      bool
	}{
		{"claude", `{"type":"result","subtype":"success","is_error":false}`, true},
		{"claude", `{"type":"result","subtype":"error_max_turns","is_error":true}`, false},
		{"claude", `{"type":"assistant"}`, false},
		{"codex", `{"type":"turn.completed"}`, true},
		{"codex", "{\"type\":\"turn.completed\"}\n{\"type\":\"turn.failed\"}", false},
		{"codex", `{"type":"turn.started"}`, false},
		{"codex", `{"type":"turn.completed"`, false},
	} {
		if got := nativeCLISucceeded(item.kind, item.output); got != item.success {
			t.Fatalf("%s %s = %v", item.kind, item.output, got)
		}
	}
	ctx := context.Background()
	for _, kind := range []string{"claude", "codex"} {
		h, _ := newNativeCLI(kind, NativeCLIOptions{EgressManaged: true, Timeout: time.Minute})
		fake := sandbox.NewFake()
		sb, _ := fake.Create(ctx, sandbox.Spec{})
		_ = h.ConfigureModelEndpoint(ctx, sb, ModelEndpoint{})
		result, err := h.Run(ctx, sb, Task{Prompt: "fix", RepositoryDir: "/repo"})
		if err != nil || result.ExitCode == 0 {
			t.Fatalf("missing terminal accepted: %+v %v", result, err)
		}
		fake.ExecuteFunc = func(sandbox.Command) (sandbox.Execution, error) {
			return sandbox.Execution{ExitCode: 124, TimedOut: true}, nil
		}
		result, err = h.Run(ctx, sb, Task{Prompt: "fix", RepositoryDir: "/repo"})
		if err != nil || result.ExitCode != 124 || !result.TimedOut {
			t.Fatal("timeout lost")
		}
	}
}

func TestNativeCLIEndpointValidation(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		h, _ := newNativeCLI(kind, NativeCLIOptions{EgressManaged: true})
		for _, endpoint := range []ModelEndpoint{
			{Provider: "incompatible"}, {BaseURL: "https://wrong.example/v1"},
			{BaseURL: "https://user:secret@example.test/v1"}, {APIKeyEnv: "BAD=KEY"}, {APIKeyEnv: "OTHER_CREDENTIAL"}, {Model: "--bad"},
		} {
			if err := h.ValidateModelEndpoint(endpoint); err == nil {
				t.Fatalf("accepted %+v", endpoint)
			}
		}
		if _, err := newNativeCLI(kind, NativeCLIOptions{EgressManaged: true, BaseURL: "http://gateway.test/v1"}); err == nil {
			t.Fatal("managed credentials allowed over HTTP")
		}
	}
}
