package agentharness

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// TestUnrealGenericSpecShape locks the zero-dependency contract for the
// unreal-agent runner: generic harness, stdin prompt delivery, fixed sh
// wrapper, credential allowlist only. The gateway URL travels via pass_env
// from the worker environment, never as harness endpoint wiring.
func TestUnrealGenericSpecShape(t *testing.T) {
	t.Parallel()

	spec := Spec{
		Name:       "unreal",
		Executable: "/bin/sh",
		Args:       []string{"-c", `export THINKING_LEVEL="${THINKING_LEVEL:-high}"; python3 -c 'import os,sys,json; sys.stdout.write(json.dumps({"prompt": sys.stdin.read(), "thinking_level": os.environ["THINKING_LEVEL"]}))' | exec /usr/local/bin/unreal -session-directory /tmp/unreal-sessions -log-directory /tmp/unreal-logs`},
		Model:      "vendor/model",
		Timeout:    30 * time.Minute,
		PassEnv: []string{
			"UNREAL_HARNESS_LLM_PROVIDER",
			"UNREAL_HARNESS_LLM_BASE_URL",
			"UNREAL_HARNESS_LLM_MODEL",
			"THINKING_LEVEL",
		},
		PromptMode: PromptStdinFile,
		Provision: Provision{
			Packages:     []string{"python3"},
			BinarySource: "/host/unreal-agent-runner",
			BinaryDest:   "/usr/local/bin/unreal",
			VerifyArgs:   []string{"unreal", "--version"},
		},
	}

	h, err := NewGeneric(spec)
	if err != nil {
		t.Fatalf("NewGeneric: %v", err)
	}
	if h.Name() != "unreal" {
		t.Fatalf("Name() = %q, want unreal", h.Name())
	}
	got := h.Spec()
	if got.Executable != "/bin/sh" {
		t.Fatalf("Executable = %q, want /bin/sh", got.Executable)
	}
	if got.PromptMode != PromptStdinFile {
		t.Fatalf("PromptMode = %q, want stdin_file", got.PromptMode)
	}
	if len(got.PassEnv) != 4 {
		t.Fatalf("PassEnv = %v, want the 4 approved credential names", got.PassEnv)
	}
	for _, want := range []string{"UNREAL_HARNESS_LLM_PROVIDER", "UNREAL_HARNESS_LLM_BASE_URL", "UNREAL_HARNESS_LLM_MODEL", "THINKING_LEVEL"} {
		found := false
		for _, name := range got.PassEnv {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("PassEnv = %v, missing %q", got.PassEnv, want)
		}
	}
	if got.Provision.BinaryDest != "/usr/local/bin/unreal" {
		t.Fatalf("BinaryDest = %q, want /usr/local/bin/unreal", got.Provision.BinaryDest)
	}
}

// TestUnrealPromptNeverReachesArgv proves the untrusted task text is delivered
// by stdin redirection, not interpolated into the fixed sh wrapper.
func TestUnrealPromptNeverReachesArgv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	h, err := NewGeneric(Spec{
		Name:       "unreal",
		Executable: "/bin/sh",
		Args:       []string{"-c", "exec /usr/local/bin/unreal"},
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
	const prompt = `Change "hello"; rm -rf / && $(whoami)`
	result, err := h.Run(ctx, sb, Task{
		Prompt:        prompt,
		PromptPath:    "/workspace/.factory/agent-prompt.txt",
		RepositoryDir: "/workspace/repository",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(result.CommandLine, "rm -rf") {
		t.Fatalf("untrusted prompt leaked into command line: %q", result.CommandLine)
	}
	if !strings.Contains(result.CommandLine, "/workspace/.factory/agent-prompt.txt") {
		t.Fatalf("command line should show stdin redirection: %q", result.CommandLine)
	}
}
