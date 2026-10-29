package repository

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRegisteredLocalPathMustBeGitRoot(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, bare := range []bool{false, true} {
		name := "worktree"
		if bare {
			name = "bare.git"
		}
		root := filepath.Join(dir, name)
		args := []string{"init"}
		if bare {
			args = append(args, "--bare")
		}
		args = append(args, root)
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git init: %s %v", out, err)
		}
		p := New(nil, WithLocalPaths([]string{root}))
		if err := p.validateLocalGitRoot(ctx, root); err != nil {
			t.Fatalf("registered root: %v", err)
		}
		child := filepath.Join(root, "child")
		if err := os.Mkdir(child, 0700); err != nil {
			t.Fatal(err)
		}
		p = New(nil, WithLocalPaths([]string{child}))
		calledBundle := false
		p.runHostCommand = func(ctx context.Context, dir string, args ...string) (string, error) {
			if args[0] == "bundle" {
				calledBundle = true
				t.Fatal("bundled parent of registered directory")
			}
			return runGitHost(ctx, dir, args...)
		}
		if _, err := p.Prepare(ctx, nil, Request{Kind: SourceLocal, LocalPath: child, Revision: "HEAD"}); !errors.Is(err, ErrRepositoryRejected) {
			t.Fatalf("child accepted: %v", err)
		}
		if calledBundle {
			t.Fatal("bundle attempted")
		}
	}
}

func TestHostGitIgnoresAmbientRepositoryOverrides(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "approved")
	other := filepath.Join(dir, "other")
	for _, path := range []string{root, other} {
		if out, err := exec.Command("git", "init", path).CombinedOutput(); err != nil {
			t.Fatalf("git init: %s %v", out, err)
		}
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	if err := New(nil).validateLocalGitRoot(context.Background(), root); err != nil {
		t.Fatalf("ambient Git environment changed source: %v", err)
	}
}
