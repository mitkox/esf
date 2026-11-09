package factory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

// ActorEvidence is the forge-resolved identity bound to a pipeline run.
//
// The descriptor is the API-resolved identity used for the repository write
// check. A delivery's display name is never copied here: the forge API is the
// only source of an actor that may trigger or authorize work.
type ActorEvidence struct {
	ID                   string `json:"id,omitempty"`
	Descriptor           string `json:"descriptor"`
	DisplayName          string `json:"display_name,omitempty"`
	UniqueName           string `json:"unique_name,omitempty"`
	RepositoryPermission string `json:"repository_permission,omitempty"`
}

// actorEvidenceFor converts a resolved forge actor into run evidence.
func actorEvidenceFor(actor forge.Actor, permission string) *ActorEvidence {
	evidence := &ActorEvidence{
		ID:                   strings.TrimSpace(actor.ID),
		Descriptor:           strings.TrimSpace(actor.Descriptor),
		DisplayName:          strings.TrimSpace(actor.DisplayName),
		UniqueName:           strings.TrimSpace(actor.UniqueName),
		RepositoryPermission: strings.TrimSpace(permission),
	}
	if evidence.ID == "" && evidence.Descriptor == "" && evidence.UniqueName == "" {
		return nil
	}
	return evidence
}

// manifestActor returns the forge actor and repository permission bound to a
// run, when the run came from a work item.
func manifestActor(manifest RunManifest) (actor, permission string) {
	if manifest.WorkItem == nil || manifest.WorkItem.Actor == nil {
		return "", ""
	}
	return strings.TrimSpace(manifest.WorkItem.Actor.Descriptor),
		strings.TrimSpace(manifest.WorkItem.Actor.RepositoryPermission)
}

// AmbiguousScopeError reports a work item that matches more than one
// operator-declared scope. Guessing between team boundaries is never safe, so
// the mapping is rejected and the operator fixes the configuration.
type AmbiguousScopeError struct {
	Pipeline string
	Scopes   []string
}

// Error implements error.
func (e *AmbiguousScopeError) Error() string {
	where := "the operator mapping"
	if pipeline := strings.TrimSpace(e.Pipeline); pipeline != "" {
		where = fmt.Sprintf("pipeline %q", pipeline)
	}
	return fmt.Sprintf("%s maps this work item to multiple scopes (%s); the mapping is ambiguous",
		where, strings.Join(e.Scopes, ", "))
}

// ResolveScope resolves one work item through a named pipeline to exactly one
// operator-declared scope.
func (c Config) ResolveScope(name string, fields forge.WorkItemFields) (string, error) {
	name = strings.TrimSpace(name)
	pipeline, ok := c.Pipelines[name]
	if !ok {
		return "", fmt.Errorf("pipeline %q is not configured", name)
	}
	if problems := c.pipelineProblems(pipeline); len(problems) > 0 {
		return "", fmt.Errorf("pipeline %q: %s", name, strings.Join(problems, "; "))
	}
	return pipeline.resolveScope(name, fields)
}

// ResolveScopeForConnector resolves a work item through the operator pipelines
// declared for a connector to exactly one scope.
//
// Zero matching scopes and several matching scopes are both configuration
// errors. The caller records the rejection before any run is proposed.
func (c Config) ResolveScopeForConnector(connector string, fields forge.WorkItemFields) (string, error) {
	connector = strings.TrimSpace(connector)
	if connector == "" {
		return "", fmt.Errorf("connector name is required")
	}
	scopes := map[string]struct{}{}
	var failures []string
	matched := 0
	for _, name := range sortedForgeNames(c.Pipelines) {
		pipeline := c.Pipelines[name]
		if strings.TrimSpace(pipeline.Connector) != connector {
			continue
		}
		scope, err := c.ResolveScope(name, fields)
		if err != nil {
			var ambiguous *AmbiguousScopeError
			if errors.As(err, &ambiguous) {
				return "", err
			}
			failures = append(failures, err.Error())
			continue
		}
		matched++
		scopes[scope] = struct{}{}
	}
	if matched == 0 {
		if len(failures) > 0 {
			return "", fmt.Errorf("no operator pipeline maps connector %q: %s", connector, strings.Join(failures, "; "))
		}
		return "", fmt.Errorf("no operator pipeline is configured for connector %q", connector)
	}
	names := make([]string, 0, len(scopes))
	for scope := range scopes {
		names = append(names, scope)
	}
	sort.Strings(names)
	if len(names) > 1 {
		return "", &AmbiguousScopeError{Scopes: names}
	}
	return names[0], nil
}

// matchingScopes returns every scope a matching rule declares, in stable order.
//
// It deliberately considers every matching rule, not only the most specific
// one: a more specific rule must not let a work item cross from one team's
// scope into another.
func (p PipelineConfig) matchingScopes(fields forge.WorkItemFields) []string {
	seen := map[string]struct{}{}
	for _, rule := range p.Rules {
		if _, ok := rule.match(fields); !ok {
			continue
		}
		seen[strings.TrimSpace(rule.Scope)] = struct{}{}
	}
	scopes := make([]string, 0, len(seen))
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes
}

// resolveScope resolves the unique scope for one pipeline and work item.
func (p PipelineConfig) resolveScope(pipeline string, fields forge.WorkItemFields) (string, error) {
	scopes := p.matchingScopes(fields)
	if len(scopes) == 0 {
		return "", fmt.Errorf(
			"pipeline %q: no mapping rule matches project %q, area %q, type %q, state %q",
			pipeline, strings.TrimSpace(fields.Project), strings.TrimSpace(fields.Area),
			strings.TrimSpace(fields.Type), strings.TrimSpace(fields.State),
		)
	}
	if len(scopes) > 1 {
		return "", &AmbiguousScopeError{Pipeline: pipeline, Scopes: scopes}
	}
	if scopes[0] == "" {
		return "", fmt.Errorf("pipeline %q: the matching rule does not declare a scope", pipeline)
	}
	return scopes[0], nil
}

// DefaultHarness returns the default agent harness for a scope.
//
// A scope narrows the registered harness set; the first registered harness the
// scope allows wins. An empty scope list inherits the deployment's harnesses.
func (c Config) DefaultHarness(scope string, available []string) (string, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = DefaultScope
	}
	if scope != DefaultScope {
		if _, ok := c.Scopes[scope]; !ok {
			return "", fmt.Errorf("unknown scope %q", scope)
		}
	}
	if len(available) == 0 {
		return "", fmt.Errorf("no agent harness is registered on this worker")
	}
	allowed := c.Scopes[scope].Harnesses
	for _, name := range available {
		if len(allowed) == 0 || containsString(allowed, name) {
			return name, nil
		}
	}
	return "", fmt.Errorf("no registered harness is allowed in scope %q (allowed: %s)",
		scope, strings.Join(allowed, ", "))
}

// ── Per-scope admission caps ────────────────────────────────────────────────
//
// A cap is enforced before a sandbox exists. The lease is durable so two worker
// processes sharing one deployment cannot both admit past the cap, and a
// workflow that waits is retried rather than silently dropped. A lease expires
// as a last resort when a workflow is terminated without releasing it.

const (
	scopeSlotDirName       = "scope-slots"
	scopeSlotStateFileName = "state.json"
	scopeSlotLockFileName  = "state.lock"
	// A lease outlives the default review gate plus the whole-run ceiling, so a
	// long but live run cannot lose its slot; release is the normal path and
	// expiry is only a last resort for a terminated workflow.
	scopeSlotLeaseTTL      = 48 * time.Hour
	maxScopeSlotFieldBytes = 4096
	// maxScopeConcurrency bounds a scope cap to the same ceiling as the
	// deployment's worker concurrency, so a typo cannot queue a team forever.
	maxScopeConcurrency = 256
)

// scopeSlotLease is one admitted run holding a scope slot.
type scopeSlotLease struct {
	RunID      string    `json:"run_id"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// scopeSlotState is the durable admission state.
type scopeSlotState struct {
	Scopes map[string][]scopeSlotLease `json:"scopes"`
}

// scopeSlotClaim is the outcome of one admission attempt.
type scopeSlotClaim struct {
	Granted bool
	Active  int
	Limit   int
}

// scopeSlotStore durably serialises scope admission across workers.
type scopeSlotStore struct {
	state lockedState[scopeSlotState]
}

// ScopeSlotInput identifies the scope lease to release.
type ScopeSlotInput struct {
	Scope string `json:"scope"`
	RunID string `json:"run_id"`
}

func openScopeSlotStore(artifactRoot string) (*scopeSlotStore, error) {
	if strings.TrimSpace(artifactRoot) == "" {
		return nil, errors.New("scope slots: artifact root is required")
	}
	root := filepath.Join(artifactRoot, scopeSlotDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("scope slots: create %s: %w", root, err)
	}
	return &scopeSlotStore{state: lockedState[scopeSlotState]{
		root:        root,
		fileName:    scopeSlotStateFileName,
		lockName:    scopeSlotLockFileName,
		tempPattern: ".scope-slots-*",
		label:       "scope slots",
		newState: func() scopeSlotState {
			return scopeSlotState{Scopes: map[string][]scopeSlotLease{}}
		},
		normalize: func(state *scopeSlotState) {
			if state.Scopes == nil {
				state.Scopes = map[string][]scopeSlotLease{}
			}
		},
	}}, nil
}

// Acquire admits runID into scope when fewer than limit leases are active.
//
// It is idempotent by run ID, so a retried validation activity does not consume
// a second slot. Limit zero means no scope-specific cap.
func (s *scopeSlotStore) Acquire(ctx context.Context, scope, runID string, limit int, now time.Time) (scopeSlotClaim, error) {
	if err := ctx.Err(); err != nil {
		return scopeSlotClaim{}, err
	}
	if limit < 0 {
		return scopeSlotClaim{}, fmt.Errorf("scope slots: limit must not be negative")
	}
	if limit == 0 {
		return scopeSlotClaim{Granted: true, Limit: 0}, nil
	}
	scope, err := validScopeSlotField("scope", scope)
	if err != nil {
		return scopeSlotClaim{}, err
	}
	runID, err = validScopeSlotField("run id", runID)
	if err != nil {
		return scopeSlotClaim{}, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	claim := scopeSlotClaim{Limit: limit}
	err = s.state.withState(func(state *scopeSlotState) error {
		leases := pruneScopeSlotLeases(state.Scopes[scope], now)
		for _, lease := range leases {
			if lease.RunID == runID {
				claim.Granted = true
				claim.Active = len(leases)
				state.Scopes[scope] = leases
				return nil
			}
		}
		if len(leases) >= limit {
			claim.Active = len(leases)
			state.Scopes[scope] = leases
			return nil
		}
		leases = append(leases, scopeSlotLease{
			RunID:      runID,
			AcquiredAt: now,
			ExpiresAt:  now.Add(scopeSlotLeaseTTL),
		})
		state.Scopes[scope] = leases
		claim.Granted = true
		claim.Active = len(leases)
		return nil
	})
	if err != nil {
		return scopeSlotClaim{}, err
	}
	return claim, nil
}

// Release removes runID's lease. Releasing an absent lease is a no-op.
func (s *scopeSlotStore) Release(ctx context.Context, scope, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scope, err := validScopeSlotField("scope", scope)
	if err != nil {
		return err
	}
	runID, err = validScopeSlotField("run id", runID)
	if err != nil {
		return err
	}
	return s.state.withState(func(state *scopeSlotState) error {
		leases := state.Scopes[scope]
		kept := leases[:0]
		for _, lease := range leases {
			if lease.RunID != runID {
				kept = append(kept, lease)
			}
		}
		if len(kept) == 0 {
			delete(state.Scopes, scope)
		} else {
			state.Scopes[scope] = kept
		}
		return nil
	})
}

// Active counts the unexpired leases for a scope.
func (s *scopeSlotStore) Active(ctx context.Context, scope string, now time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	scope, err := validScopeSlotField("scope", scope)
	if err != nil {
		return 0, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state, err := s.state.load()
	if err != nil {
		return 0, err
	}
	return len(pruneScopeSlotLeases(state.Scopes[scope], now)), nil
}

func pruneScopeSlotLeases(leases []scopeSlotLease, now time.Time) []scopeSlotLease {
	kept := make([]scopeSlotLease, 0, len(leases))
	for _, lease := range leases {
		if lease.ExpiresAt.IsZero() || !lease.ExpiresAt.After(now) {
			continue
		}
		kept = append(kept, lease)
	}
	return kept
}

func validScopeSlotField(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("scope slots: %s is required", name)
	}
	if len(value) > maxScopeSlotFieldBytes || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", fmt.Errorf("scope slots: %s is invalid", name)
	}
	return value, nil
}

// acquireScopeSlot performs admission for a validated run.
func (a *Activities) acquireScopeSlot(ctx context.Context, scope, runID string, limit int) (scopeSlotClaim, error) {
	store, err := openScopeSlotStore(a.artifactFactory.Root())
	if err != nil {
		return scopeSlotClaim{}, err
	}
	return store.Acquire(ctx, scope, runID, limit, time.Now().UTC())
}

// ReleaseScopeSlot releases a scope lease after the run has cleaned up.
func (a *Activities) ReleaseScopeSlot(ctx context.Context, in ScopeSlotInput) error {
	store, err := openScopeSlotStore(a.artifactFactory.Root())
	if err != nil {
		return err
	}
	return store.Release(ctx, in.Scope, in.RunID)
}
