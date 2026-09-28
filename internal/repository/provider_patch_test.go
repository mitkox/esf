package repository

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/sandbox"
)

func TestPatchIncludesChangesCommittedByAgent(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@test", "commit", "-qm", "baseline")
	baseline := git("rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("committed change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@test", "commit", "-qm", "agent change")
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := sandbox.NewFake()
	fake.ExecuteFunc = func(c sandbox.Command) (sandbox.Execution, error) {
		cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
		stdout, err := cmd.Output()
		result := sandbox.Execution{Stdout: string(stdout)}
		if exit, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
			result.Stderr = string(exit.Stderr)
			return result, nil
		}
		return result, err
	}
	sb, err := fake.Create(context.Background(), sandbox.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	patch, err := New(nil).Patch(context.Background(), sb, dir, baseline)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(patch, "+committed change") || !strings.Contains(patch, "+new file") {
		t.Fatalf("patch lost committed or untracked agent changes:\n%s", patch)
	}
}
