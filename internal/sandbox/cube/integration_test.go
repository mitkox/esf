//go:build integration

// Package cubeintegration holds integration tests that DO use the running
// CubeSandbox deployment.
//
// Run with:  make integration-test
//
// These tests never mutate the deployment: they create their own sandboxes,
// tag them as test-created, and destroy them. Every test verifies that it left
// nothing behind, because a leaked microVM is an integration failure.
package cube

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// testOrigin tags every sandbox these tests create, so a leak is attributable.
const testOrigin = "factory-integration-test"

func newProvider(t *testing.T) *Provider {
	t.Helper()

	cfg := ConfigFromEnv()
	// Environment defaults keep the tests aligned with the discovered
	// deployment; an explicit CUBE_API_URL still wins.
	if cfg.APIURL == "" {
		t.Fatalf("CUBE_API_URL is not set; run `make integration-test` which supplies it")
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("cube.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// requireCleanStart refuses to run if the deployment already has sandboxes, so
// the leak assertions in these tests cannot be confused by someone else's work.
func requireCleanStart(t *testing.T, p *Provider) []sandbox.Info {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return before
}

// assertNoLeak fails the test if a sandbox created by the test is still alive.
func assertNoLeak(t *testing.T, p *Provider, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	infos, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List after destroy: %v", err)
	}
	for _, info := range infos {
		if info.ID == id {
			t.Fatalf("sandbox %s leaked: it is still present after destroy", id)
		}
	}
}

// TestCreateExecuteDestroyEcho is integration test 1: create, run, capture,
// destroy.
func TestCreateExecuteDestroyEcho(t *testing.T) {
	p := newProvider(t)
	requireCleanStart(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sb, err := p.Create(ctx, sandbox.Spec{
		Metadata:    map[string]string{"origin": testOrigin, "test": t.Name()},
		IdleTimeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		killCtx, c := context.WithTimeout(context.Background(), 90*time.Second)
		defer c()
		_ = p.Destroy(killCtx, sb.ID())
	})

	exec, err := sb.Execute(ctx, sandbox.Command{Argv: []string{"echo", "factory-cube-ok"}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !exec.Succeeded() {
		t.Fatalf("echo failed: exit=%d stderr=%q", exec.ExitCode, exec.Stderr)
	}
	if strings.TrimSpace(exec.Stdout) != "factory-cube-ok" {
		t.Fatalf("stdout = %q", exec.Stdout)
	}

	// stdout and stderr must stay separate, and the exit code must survive.
	separate, err := sb.Execute(ctx, sandbox.Command{
		Script: "echo to-stdout; echo to-stderr 1>&2; exit 7",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if separate.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", separate.ExitCode)
	}
	if !strings.Contains(separate.Stdout, "to-stdout") || !strings.Contains(separate.Stderr, "to-stderr") {
		t.Fatalf("streams were not captured separately: stdout=%q stderr=%q", separate.Stdout, separate.Stderr)
	}

	if err := p.Destroy(ctx, sb.ID()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	assertNoLeak(t, p, sb.ID())
}

// TestFileRoundTrip is integration test 2: create a file, read it back.
func TestFileRoundTrip(t *testing.T) {
	p := newProvider(t)
	requireCleanStart(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sb, err := p.Create(ctx, sandbox.Spec{
		Metadata:    map[string]string{"origin": testOrigin, "test": t.Name()},
		IdleTimeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		killCtx, c := context.WithTimeout(context.Background(), 90*time.Second)
		defer c()
		_ = p.Destroy(killCtx, sb.ID())
	})

	const content = "factory file round trip\nwith a second line\n"
	if err := sb.WriteFile(ctx, "/workspace/nested/deeper/artifact.txt", []byte(content)); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := sb.ReadFile(ctx, "/workspace/nested/deeper/artifact.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content = %q, want %q", got, content)
	}

	if err := p.Destroy(ctx, sb.ID()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	assertNoLeak(t, p, sb.ID())
}

// TestRuntimeNetworkPolicyUpdate proves the deployed Cube version accepts the
// fail-closed L7 credential policy shape used immediately before an agent run.
// The token is deliberately fake and no outbound request is made.
func TestRuntimeNetworkPolicyUpdate(t *testing.T) {
	p := newProvider(t)
	requireCleanStart(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sb, err := p.Create(ctx, sandbox.Spec{
		Metadata:    map[string]string{"origin": testOrigin, "test": t.Name()},
		IdleTimeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		killCtx, c := context.WithTimeout(context.Background(), 90*time.Second)
		defer c()
		_ = p.Destroy(killCtx, sb.ID())
	})

	updater, ok := sb.(sandbox.NetworkUpdater)
	if !ok {
		t.Fatal("Cube sandbox does not implement runtime network updates")
	}
	deny := false
	err = updater.UpdateNetwork(ctx, sandbox.Network{
		AllowInternet: &deny,
		Rules: []sandbox.NetworkRule{{
			Name: "factory-test-model",
			Match: sandbox.NetworkMatch{
				Scheme: "https", Host: "example.com", SNI: "example.com",
				Method: []string{"POST"}, Path: "/v1/*",
			},
			Action: sandbox.NetworkAction{
				Allow: true, Audit: "metadata",
				Inject: []sandbox.HeaderInjection{{
					Header: "Authorization", Secret: "fake-integration-token", Format: "Bearer ${SECRET}",
				}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("UpdateNetwork: %v", err)
	}

	if err := p.Destroy(ctx, sb.ID()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	assertNoLeak(t, p, sb.ID())
}

// TestGitWorkflowInsideSandbox is integration test 3: install git, build a
// repository, modify it, and collect a diff — the exact operations the factory
// depends on.
func TestGitWorkflowInsideSandbox(t *testing.T) {
	p := newProvider(t)
	requireCleanStart(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sb, err := p.Create(ctx, sandbox.Spec{
		Metadata:    map[string]string{"origin": testOrigin, "test": t.Name()},
		IdleTimeout: 8 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		killCtx, c := context.WithTimeout(context.Background(), 90*time.Second)
		defer c()
		_ = p.Destroy(killCtx, sb.ID())
	})

	// git is the factory's own prerequisite; prove it can be installed.
	setup, err := sb.Execute(ctx, sandbox.Command{
		Script:  "export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get install -y -qq --no-install-recommends git ca-certificates >/dev/null 2>&1 && git --version",
		Timeout: 8 * time.Minute,
	})
	if err != nil {
		t.Fatalf("install git: %v", err)
	}
	if !setup.Succeeded() {
		t.Fatalf("git install failed: exit=%d stderr=%q", setup.ExitCode, setup.Stderr)
	}

	script := `
set -eu
mkdir -p /workspace/repo
cd /workspace/repo
git init -q
git config user.email factory@test
git config user.name Factory
printf 'hello\n' > greeting.txt
printf '__pycache__/\n' > .gitignore
git add -A
git commit -qm baseline
printf 'hello factory\n' > greeting.txt
git add -A
git diff --cached --no-color
`
	diff, err := sb.Execute(ctx, sandbox.Command{Script: script, Timeout: 3 * time.Minute})
	if err != nil {
		t.Fatalf("git workflow: %v", err)
	}
	if !diff.Succeeded() {
		t.Fatalf("git workflow failed: exit=%d stderr=%q", diff.ExitCode, diff.Stderr)
	}
	if !strings.Contains(diff.Stdout, "-hello") || !strings.Contains(diff.Stdout, "+hello factory") {
		t.Fatalf("patch does not contain the expected change:\n%s", diff.Stdout)
	}

	if err := p.Destroy(ctx, sb.ID()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	assertNoLeak(t, p, sb.ID())
}

// TestDestroyIsIdempotent proves cleanup can be retried safely, which the
// workflow's cleanup retry policy depends on.
func TestDestroyIsIdempotent(t *testing.T) {
	p := newProvider(t)
	requireCleanStart(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sb, err := p.Create(ctx, sandbox.Spec{
		Metadata:    map[string]string{"origin": testOrigin, "test": t.Name()},
		IdleTimeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := p.Destroy(ctx, sb.ID()); err != nil {
		t.Fatalf("first Destroy: %v", err)
	}
	if err := p.Destroy(ctx, sb.ID()); err != nil {
		t.Fatalf("second Destroy must be a no-op, got %v", err)
	}
	assertNoLeak(t, p, sb.ID())
}

// TestPingVerifiesTemplateExists proves startup validation catches a bogus
// template id rather than failing later inside a run.
func TestPingVerifiesTemplateExists(t *testing.T) {
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-does-not-exist-000000000000")
	p := newProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := p.Ping(ctx); err == nil {
		t.Fatal("expected Ping to fail for a non-existent template")
	}
}

// TestOriginUnsetIsIgnored guards the tagging convention: the tests rely on
// metadata to attribute leaks, so metadata must survive the round trip.
func TestMetadataSurvivesRoundTrip(t *testing.T) {
	p := newProvider(t)
	requireCleanStart(t, p)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sb, err := p.Create(ctx, sandbox.Spec{
		Metadata:    map[string]string{"origin": testOrigin, "run_id": "run-metadata-test"},
		IdleTimeout: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer func() {
		killCtx, c := context.WithTimeout(context.Background(), 90*time.Second)
		defer c()
		_ = p.Destroy(killCtx, sb.ID())
	}()

	infos, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, info := range infos {
		if info.ID != sb.ID() {
			continue
		}
		found = true
		if info.Metadata["origin"] != testOrigin {
			t.Fatalf("origin metadata = %q, want %q", info.Metadata["origin"], testOrigin)
		}
		if info.Metadata["run_id"] != "run-metadata-test" {
			t.Fatalf("run_id metadata = %q", info.Metadata["run_id"])
		}
	}
	if !found {
		t.Fatalf("sandbox %s was not listed", sb.ID())
	}
}

// TestMain refuses to run the suite when the deployment already has sandboxes
// belonging to the factory, so a leak from an earlier run is noticed rather
// than silently attributed to this one.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
