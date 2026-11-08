package factory

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/sandbox"
	"go.temporal.io/sdk/testsuite"
)

// TestResolveScopeForConnectorRequiresExactlyOneScope proves the tenancy
// boundary is never guessed: no match and several matches are both rejected,
// while one match resolves.
func TestResolveScopeForConnectorRequiresExactlyOneScope(t *testing.T) {
	t.Parallel()

	cfg := forgeTestConfig()
	cfg.Scopes["identity"] = ScopeConfig{}
	rule := func(project, scope string) PipelineRule {
		return PipelineRule{
			Project:             project,
			Scope:               scope,
			Repository:          "https://operator.example/repo",
			BaseBranch:          "main",
			VerificationProfile: "default",
		}
	}

	cfg.Pipelines = map[string]PipelineConfig{
		"bugfix": {Connector: "azdo", Rules: []PipelineRule{rule("Payments", "payments")}},
	}
	scope, err := cfg.ResolveScopeForConnector("azdo", forge.WorkItemFields{Project: "Payments"})
	if err != nil || scope != "payments" {
		t.Fatalf("ResolveScopeForConnector() = %q, %v", scope, err)
	}

	// The same work item matching a second scope is ambiguous even when one
	// rule is more specific: a more specific rule must not cross a team
	// boundary.
	cfg.Pipelines["bugfix"] = PipelineConfig{Connector: "azdo", Rules: []PipelineRule{
		rule("Payments", "payments"),
		{
			Project:             "Payments",
			Type:                "Bug",
			Scope:               "identity",
			Repository:          "https://operator.example/identity",
			BaseBranch:          "main",
			VerificationProfile: "default",
		},
	}}
	var ambiguous *AmbiguousScopeError
	if _, err := cfg.ResolveScopeForConnector("azdo", forge.WorkItemFields{Project: "Payments", Type: "Bug"}); !errors.As(err, &ambiguous) {
		t.Fatalf("ResolveScopeForConnector() = %v, want an ambiguous-scope error", err)
	}
	if _, err := cfg.ResolvePipeline("bugfix", forge.WorkItemFields{Project: "Payments", Type: "Bug"}); !errors.As(err, &ambiguous) {
		t.Fatalf("ResolvePipeline() = %v, want an ambiguous-scope error", err)
	}

	if _, err := cfg.ResolveScopeForConnector("azdo", forge.WorkItemFields{Project: "Identity"}); err == nil {
		t.Fatal("ResolveScopeForConnector accepted a work item with no scope mapping")
	}
}

// TestScopeNarrowsVerificationAndHarness proves a scope actually selects the
// harness and verification profile a run uses, rather than only recording them.
func TestScopeNarrowsVerificationAndHarness(t *testing.T) {
	t.Parallel()

	cfg := resourceConfig()
	req := RunRequest{
		RunID:               "run-1",
		Scope:               "payments",
		Repository:          "https://github.com/acme/payments",
		Revision:            "abc",
		Task:                "fix it",
		AgentHarness:        "opencode2",
		VerificationProfile: "default",
	}
	resolved, err := cfg.ResolveResources(req)
	if err != nil {
		t.Fatalf("ResolveResources: %v", err)
	}
	if resolved.VerificationProfile != "default" || resolved.MaxConcurrentRuns != 2 {
		t.Fatalf("resolved scope = %#v", resolved)
	}

	harness, err := cfg.DefaultHarness("payments", []string{"other", "opencode2"})
	if err != nil || harness != "opencode2" {
		t.Fatalf("DefaultHarness() = %q, %v", harness, err)
	}
	cfg.Scopes["payments"] = ScopeConfig{Harnesses: []string{"missing"}}
	if _, err := cfg.DefaultHarness("payments", []string{"opencode2"}); err == nil {
		t.Fatal("DefaultHarness accepted a harness outside the scope's set")
	}

	if _, err := resourceConfig().ResolveResources(RunRequest{
		Scope: "payments", Repository: "https://github.com/acme/payments",
		Revision: "abc", Task: "fix it", AgentHarness: "opencode2",
		VerificationProfile: "other",
	}); err == nil {
		t.Fatal("ResolveResources accepted a verification profile outside the scope")
	}
}

// TestScopeSlotStoreCapsWaitsAndReleases proves the admission store refuses an
// over-cap run without dropping it, is idempotent by run, and frees the slot
// only on release.
func TestScopeSlotStoreCapsWaitsAndReleases(t *testing.T) {
	t.Parallel()

	store, err := openScopeSlotStore(t.TempDir())
	if err != nil {
		t.Fatalf("openScopeSlotStore: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	first, err := store.Acquire(ctx, "payments", "run-1", 1, now)
	if err != nil || !first.Granted || first.Active != 1 {
		t.Fatalf("first Acquire() = %#v, %v", first, err)
	}
	retry, err := store.Acquire(ctx, "payments", "run-1", 1, now)
	if err != nil || !retry.Granted || retry.Active != 1 {
		t.Fatalf("idempotent Acquire() = %#v, %v", retry, err)
	}
	queued, err := store.Acquire(ctx, "payments", "run-2", 1, now)
	if err != nil {
		t.Fatalf("queued Acquire: %v", err)
	}
	if queued.Granted || queued.Active != 1 || queued.Limit != 1 {
		t.Fatalf("over-cap Acquire() = %#v, want a wait result", queued)
	}
	if active, err := store.Active(ctx, "payments", now); err != nil || active != 1 {
		t.Fatalf("Active() = %d, %v", active, err)
	}
	if err := store.Release(ctx, "payments", "run-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	admitted, err := store.Acquire(ctx, "payments", "run-2", 1, now)
	if err != nil || !admitted.Granted || admitted.Active != 1 {
		t.Fatalf("post-release Acquire() = %#v, %v", admitted, err)
	}
}

// TestScopeSlotStoreExpiresStaleLeases keeps a terminated run from blocking a
// team forever while still bounding an ordinary run.
func TestScopeSlotStoreExpiresStaleLeases(t *testing.T) {
	t.Parallel()

	store, err := openScopeSlotStore(t.TempDir())
	if err != nil {
		t.Fatalf("openScopeSlotStore: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if claim, err := store.Acquire(ctx, "payments", "run-old", 1, now); err != nil || !claim.Granted {
		t.Fatalf("Acquire() = %#v, %v", claim, err)
	}
	if claim, err := store.Acquire(ctx, "payments", "run-new", 1, now.Add(scopeSlotLeaseTTL+time.Minute)); err != nil || !claim.Granted {
		t.Fatalf("Acquire() after expiry = %#v, %v", claim, err)
	}
}

// TestPublicationAuditBindsForgeActor proves publication actions carry the
// forge-resolved actor and repository permission in the audit trail.
func TestPublicationAuditBindsForgeActor(t *testing.T) {
	t.Parallel()

	factory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("artifact store: %v", err)
	}
	store, err := factory.ForRun("run-1")
	if err != nil {
		t.Fatalf("ForRun: %v", err)
	}
	acts := &Activities{
		artifactFactory: factory,
		redactor:        NewRedactor(),
		log:             slog.New(slog.DiscardHandler),
	}
	manifest := RunManifest{
		RunID: "run-1",
		WorkItem: &WorkItemEvidence{
			Connector: "azdo",
			ID:        "42",
			Actor: &ActorEvidence{
				ID:                   "aad-1",
				Descriptor:           "aad.ada-lovelace",
				RepositoryPermission: forge.PermissionWrite,
			},
		},
	}
	if err := acts.appendPublicationAudit(store, manifest, "SUCCESS", "", nil); err != nil {
		t.Fatalf("appendPublicationAudit: %v", err)
	}
	entries, err := ReadAudit(store)
	if err != nil {
		t.Fatalf("ReadAudit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %#v", entries)
	}
	entry := entries[0]
	if entry.Actor != "aad.ada-lovelace" || entry.ActorKind != ActorKindForge {
		t.Fatalf("audit actor = %q/%q", entry.Actor, entry.ActorKind)
	}
	if entry.ActorPermission != forge.PermissionWrite {
		t.Fatalf("audit permission = %q", entry.ActorPermission)
	}
	if got := entry.Detail["repository_permission"]; got != forge.PermissionWrite {
		t.Fatalf("audit detail = %#v", entry.Detail)
	}
}

// TestScopeConcurrencyCapQueuesUntilRelease proves a run at a full scope waits
// for admission instead of being dropped: the workflow starts no sandbox until
// the lease is released, then completes normally.
func TestScopeConcurrencyCapQueuesUntilRelease(t *testing.T) {
	artifactDir := t.TempDir()
	store, err := openScopeSlotStore(artifactDir)
	if err != nil {
		t.Fatalf("openScopeSlotStore: %v", err)
	}
	if claim, err := store.Acquire(context.Background(), DefaultScope, "other-run", 1, time.Now().UTC()); err != nil || !claim.Granted {
		t.Fatalf("pre-acquire = %#v, %v", claim, err)
	}

	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	released := false
	var releaseErr error
	manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{
		artifactDir: artifactDir,
		mutateConfig: func(cfg *Config) {
			cfg.Scopes = map[string]ScopeConfig{
				DefaultScope: {MaxConcurrentRuns: 1},
			}
		},
	}, func(env *testsuite.TestWorkflowEnvironment) {
		env.RegisterDelayedCallback(func() {
			releaseErr = store.Release(context.Background(), DefaultScope, "other-run")
			released = true
		}, 100*time.Millisecond)
	})
	if err != nil {
		t.Fatalf("workflow returned an error: %v", err)
	}
	if !released {
		t.Fatal("the workflow never waited for the scope slot")
	}
	if releaseErr != nil {
		t.Fatalf("release the held slot: %v", releaseErr)
	}
	if manifest.FactoryResult != StateSucceeded {
		t.Fatalf("factory_result = %s (%s)", manifest.FactoryResult, manifest.Error)
	}
	if created := fake.Created(); len(created) != 1 {
		t.Fatalf("sandboxes created = %v, want one after admission", created)
	}
	if live := fake.Live(); len(live) != 0 {
		t.Fatalf("sandbox leaked: %v", live)
	}
	active, err := store.Active(context.Background(), DefaultScope, time.Now().UTC())
	if err != nil || active != 0 {
		t.Fatalf("scope still holds %d leases (%v)", active, err)
	}
}
