package factory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/artifacts"
	"github.com/mitkox/esf/internal/repository"
)

// A Change is the factory's durable unit of work.
//
// This is the single most valuable idea taken from Google's AX: a Task there is
// a long-lived object with a lifecycle, and the process that runs it is an
// implementation detail. The MVP conflated the two — a RunManifest was both the
// work item and the execution record — which meant a review-and-rework cycle
// was a brand-new run with no lineage, no aggregate cost, and no way to answer
// "what happened to this change across three attempts".
//
// A Change owns identity; Runs are activations of it. The run manifest stays
// authoritative for what happened in one execution, and the change record is
// the parent that makes a sequence of executions legible.
type Change struct {
	ChangeID string `json:"change_id"`
	// Scope is the tenancy boundary the change belongs to.
	Scope string `json:"scope,omitempty"`
	// Repository and Revision are the target. A rework activation must use the
	// same repository; the revision may advance if the operator says so, and
	// each activation records its own baseline.
	Repository        string `json:"repository"`
	RequestedRevision string `json:"requested_revision"`
	// Task is the change's description. It is carried forward to rework
	// activations unless the operator supplies a new one.
	Task     string `json:"task"`
	TaskHash string `json:"task_hash"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Status is the human-facing lifecycle of the change.
	Status ChangeStatus `json:"status"`
	// Activations are the runs that belong to this change, oldest first.
	Activations []Activation `json:"activations"`

	// ── Aggregates over activations ─────────────────────────────────────────

	// TotalCostUSD and TotalTokens sum whatever the harnesses actually
	// reported. A missing report contributes zero and is never estimated.
	TotalCostUSD float64 `json:"total_cost_usd"`
	TotalTokens  int64   `json:"total_tokens"`
	// Attempts counts activations.
	Attempts int `json:"attempts"`
	// LastRunID is the most recent activation.
	LastRunID string `json:"last_run_id,omitempty"`
	// LastResult is the most recent activation's factory result.
	LastResult RunState `json:"last_result,omitempty"`
	// ReworkNote is the human instruction that triggered the latest rework.
	ReworkNote string `json:"rework_note,omitempty"`
}

// ChangeStatus is the lifecycle of a Change.
type ChangeStatus string

const (
	// ChangeOpen means the change is being worked on and may be reworked.
	ChangeOpen ChangeStatus = "OPEN"
	// ChangeInReview means an activation passed every gate and awaits a human
	// decision.
	ChangeInReview ChangeStatus = "IN_REVIEW"
	// ChangeDone means a human approved the change.
	ChangeDone ChangeStatus = "DONE"
	// ChangeAbandoned means the change was explicitly closed without shipping.
	ChangeAbandoned ChangeStatus = "ABANDONED"
)

// Valid reports whether s is a known change status.
func (s ChangeStatus) Valid() bool {
	switch s {
	case ChangeOpen, ChangeInReview, ChangeDone, ChangeAbandoned:
		return true
	default:
		return false
	}
}

// Activation is one run that belongs to a Change.
type Activation struct {
	RunID string `json:"run_id"`
	// ParentRunID is the run this activation reworks, empty for the first.
	ParentRunID string `json:"parent_run_id,omitempty"`
	// Reason is why the activation exists: "initial", "rework", "retry" or
	// "candidate".
	Reason string `json:"reason"`
	// Task is the prompt this activation used, which may differ from the
	// change's task after a rework instruction.
	Task      string    `json:"task"`
	Scope     string    `json:"scope,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Activation reason constants.
const (
	ActivationInitial   = "initial"
	ActivationRework    = "rework"
	ActivationRetry     = "retry"
	ActivationCandidate = "candidate"
)

// ChangeStore persists changes as JSON documents beside the run artifacts.
//
// It lives in the same durable root as run evidence on purpose: a change that
// outlived its evidence would be a dangling pointer, and backup/restore must
// treat them as one unit. The store holds an artifact factory so aggregate
// totals can be recomputed from the authoritative run manifests rather than
// cached and trusted.
type ChangeStore struct {
	root    string
	factory artifacts.Factory
}

// ChangeDirName is the directory under the artifact root holding changes.
const ChangeDirName = "changes"

// NewChangeStore returns a store rooted at an artifact root.
func NewChangeStore(artifactRoot string) (*ChangeStore, error) {
	if strings.TrimSpace(artifactRoot) == "" {
		return nil, errors.New("change store: artifact root is required")
	}
	factory, err := artifacts.NewLocal(artifactRoot)
	if err != nil {
		return nil, fmt.Errorf("change store: %w", err)
	}
	root := filepath.Join(factory.Root(), ChangeDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("change store: create %s: %w", root, err)
	}
	return &ChangeStore{root: root, factory: factory}, nil
}

// Root returns the directory holding change documents.
func (s *ChangeStore) Root() string { return s.root }

// pathFor validates the change ID and returns its document path.
//
// The guard mirrors internal/artifacts: a change ID is a plain path segment, so
// a caller can never escape the store.
func (s *ChangeStore) pathFor(changeID string) (string, error) {
	if changeID == "" {
		return "", errors.New("change store: change id is required")
	}
	if strings.ContainsAny(changeID, `/\`) || changeID == "." || changeID == ".." {
		return "", fmt.Errorf("change store: invalid change id %q", changeID)
	}
	return filepath.Join(s.root, changeID+".json"), nil
}

// Load reads a change. A missing change returns ErrChangeNotFound.
func (s *ChangeStore) Load(changeID string) (Change, error) {
	path, err := s.pathFor(changeID)
	if err != nil {
		return Change{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Change{}, ErrChangeNotFound
		}
		return Change{}, fmt.Errorf("change store: read %s: %w", changeID, err)
	}
	var c Change
	if err := json.Unmarshal(data, &c); err != nil {
		return Change{}, fmt.Errorf("change store: decode %s: %w", changeID, err)
	}
	return c, nil
}

// ErrChangeNotFound reports that a change does not exist.
var ErrChangeNotFound = errors.New("change not found")

// Save writes a change atomically with owner-only permissions.
func (s *ChangeStore) Save(c Change) error {
	path, err := s.pathFor(c.ChangeID)
	if err != nil {
		return err
	}
	if c.Status == "" {
		c.Status = ChangeOpen
	}
	if !c.Status.Valid() {
		return fmt.Errorf("change store: invalid status %q", c.Status)
	}
	// A change with no activations is a draft, not a record; saving one would
	// make List show work that never ran.
	if len(c.Activations) == 0 {
		return errors.New("change store: refusing to save a change with no activations")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("change store: encode %s: %w", c.ChangeID, err)
	}
	tmp, err := os.CreateTemp(s.root, c.ChangeID+".tmp-*")
	if err != nil {
		return fmt.Errorf("change store: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("change store: chmod: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("change store: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("change store: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("change store: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("change store: rename: %w", err)
	}
	return nil
}

// List returns every change, newest first.
func (s *ChangeStore) List() ([]Change, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("change store: list: %w", err)
	}
	var out []Change
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		c, err := s.Load(id)
		if err != nil {
			if errors.Is(err, ErrChangeNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// OpenChange starts a new change, or returns the existing one when it already
// has an activation for the run.
//
// Idempotency matters because a retried run submission must not create a second
// change with the same identity.
func (s *ChangeStore) OpenChange(c Change) (Change, bool, error) {
	if existing, err := s.Load(c.ChangeID); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrChangeNotFound) {
		return Change{}, false, err
	}
	if err := s.Save(c); err != nil {
		return Change{}, false, err
	}
	return c, true, nil
}

// RecordRun folds one finished run into its change.
//
// The operation is idempotent by run ID: Temporal may invoke the activity more
// than once, and a duplicated activation must not inflate cost aggregates. The
// run manifest remains authoritative for the execution; this only maintains the
// parent's identity, lineage and aggregate view.
func (s *ChangeStore) RecordRun(manifest RunManifest) (Change, error) {
	changeID := manifest.ChangeID
	if changeID == "" {
		// A run with no change still gets one, so every run has a parent and
		// the older one-run-per-task behaviour is preserved.
		changeID = manifest.RunID
	}
	change, err := s.Load(changeID)
	if err != nil {
		if !errors.Is(err, ErrChangeNotFound) {
			return Change{}, err
		}
		change = Change{
			ChangeID:          changeID,
			Scope:             manifest.Scope,
			Repository:        manifest.Repository,
			RequestedRevision: manifest.RequestedRevision,
			TaskHash:          manifest.TaskHash,
			CreatedAt:         manifest.StartedAt,
			Status:            ChangeOpen,
		}
		// The task text is only known to the caller; the manifest carries its
		// hash. The activator supplies the text when it opens the change, so a
		// change discovered only here gets the hash and an empty task.
	}

	// Idempotency: replace an existing activation rather than appending.
	var activations []Activation
	replaced := false
	for _, a := range change.Activations {
		if a.RunID == manifest.RunID {
			activations = append(activations, a)
			replaced = true
			continue
		}
		activations = append(activations, a)
	}
	if !replaced {
		activations = append(activations, Activation{
			RunID:       manifest.RunID,
			ParentRunID: manifest.ParentRunID,
			Reason:      activationReason(manifest),
			Task:        "",
			Scope:       manifest.Scope,
			CreatedAt:   manifest.StartedAt,
		})
	}
	change.Activations = activations
	change.Attempts = len(activations)
	change.UpdatedAt = manifest.CompletedAt
	if change.UpdatedAt.IsZero() {
		change.UpdatedAt = time.Now().UTC()
	}
	change.LastRunID = manifest.RunID
	change.LastResult = manifest.FactoryResult
	if manifest.HumanResult == HumanResultRejected {
		change.ReworkNote = manifest.ReviewNote
	}
	if manifest.Scope != "" {
		change.Scope = manifest.Scope
	}

	// Recompute aggregates from the manifests that are still reachable. Only
	// the current run's numbers are available here, so totals are maintained
	// incrementally and never estimated.
	recompute, err := s.recomputeTotals(change)
	if err != nil {
		return Change{}, err
	}
	change.TotalCostUSD, change.TotalTokens = recompute.cost, recompute.tokens

	change.Status = deriveChangeStatus(change, manifest)
	if err := s.Save(change); err != nil {
		return Change{}, err
	}
	return change, nil
}

// totals is the aggregate pair recomputed across activations.
type totals struct {
	cost   float64
	tokens int64
}

// recomputeTotals reads each activation's manifest and sums recorded usage.
//
// It degrades silently for activations whose manifest is missing or carries no
// usage: an unavailable number contributes zero and is never fabricated. That
// keeps a change's totals a floor, which is the honest direction for a spend
// report shown to an operator.
func (s *ChangeStore) recomputeTotals(c Change) (totals, error) {
	var t totals
	for _, a := range c.Activations {
		if !s.manifestExists(a.RunID) {
			continue
		}
		manifest, err := ReadManifest(s.factory, a.RunID)
		if err != nil {
			continue
		}
		if manifest.InferenceCost != nil {
			t.cost += *manifest.InferenceCost
		}
		if manifest.TokensIn != nil {
			t.tokens += *manifest.TokensIn
		}
		if manifest.TokensOut != nil {
			t.tokens += *manifest.TokensOut
		}
	}
	return t, nil
}

// manifestExists checks for the authoritative record without opening a run
// store. Local.ForRun creates directories by contract, which is correct for
// writers but would otherwise leave empty run directories while totals merely
// probe a missing activation.
func (s *ChangeStore) manifestExists(runID string) bool {
	if runID == "" || runID == "." || runID == ".." || strings.ContainsAny(runID, `/\`) {
		return false
	}
	_, err := os.Stat(filepath.Join(s.factory.Root(), "runs", runID, ArtifactRun))
	return err == nil
}

// deriveChangeStatus maps the latest activation onto the change lifecycle.
func deriveChangeStatus(c Change, latest RunManifest) ChangeStatus {
	switch {
	case c.Status == ChangeDone || c.Status == ChangeAbandoned:
		// A terminal human decision is never overwritten by a later run; an
		// operator must reopen the change explicitly.
		return c.Status
	case latest.HumanResult == HumanResultApproved:
		return ChangeDone
	case latest.HumanResult == HumanResultRejected:
		return ChangeOpen
	case latest.FactoryResult == StateSucceeded:
		return ChangeInReview
	default:
		return ChangeOpen
	}
}

// activationReason classifies a run's relationship to its change.
func activationReason(m RunManifest) string {
	if m.ParentRunID != "" {
		return ActivationRework
	}
	if m.CandidateID != "" {
		return ActivationCandidate
	}
	return ActivationInitial
}

// Human review outcomes recorded in a manifest.
const (
	HumanResultApproved = "approved"
	HumanResultRejected = "rejected"
	HumanResultTimeout  = "timeout"
)

// NewChange builds the initial change for a run request.
func NewChange(req RunRequest, resolved ResolvedResources, now time.Time) Change {
	taskHash := repository.HashTask(req.Task)
	changeID := req.ChangeID
	if changeID == "" {
		changeID = req.RunID
	}
	return Change{
		ChangeID:          changeID,
		Scope:             resolved.Scope,
		Repository:        req.Repository,
		RequestedRevision: req.Revision,
		Task:              req.Task,
		TaskHash:          taskHash,
		CreatedAt:         now,
		UpdatedAt:         now,
		Status:            ChangeOpen,
		Attempts:          1,
		LastRunID:         req.RunID,
		Activations: []Activation{{
			RunID:       req.RunID,
			ParentRunID: req.ParentRunID,
			Reason:      ActivationInitial,
			Task:        req.Task,
			Scope:       resolved.Scope,
			CreatedAt:   now,
		}},
	}
}

// AppendActivation returns the change with a new activation appended.
//
// It is used for rework submissions, where the parent is known before the run
// exists. Idempotent by run ID, for the same reason RecordRun is.
func AppendActivation(c Change, runID, parentRunID, reason, task, scope string, now time.Time) Change {
	for _, a := range c.Activations {
		if a.RunID == runID {
			return c
		}
	}
	if reason == "" {
		reason = ActivationRework
	}
	c.Activations = append(c.Activations, Activation{
		RunID:       runID,
		ParentRunID: parentRunID,
		Reason:      reason,
		Task:        task,
		Scope:       scope,
		CreatedAt:   now,
	})
	c.Attempts = len(c.Activations)
	c.LastRunID = runID
	c.UpdatedAt = now
	c.Status = ChangeOpen
	return c
}
