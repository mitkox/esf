package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

const (
	publicationDirName       = "publication"
	publicationStateFileName = "state.json"
	publicationStateLockName = "state.lock"
	publicationPendingPrefix = "pending:"
)

type publicationRecord struct {
	ChangeID       string `json:"change_id"`
	RunID          string `json:"run_id"`
	Connector      string `json:"connector"`
	Repository     string `json:"repository"`
	BaseBranch     string `json:"base_branch,omitempty"`
	Branch         string `json:"branch"`
	PullRequestID  string `json:"pull_request_id"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
	Commit         string `json:"commit,omitempty"`
	Tree           string `json:"tree,omitempty"`
	BundleDigest   string `json:"bundle_digest,omitempty"`
	Published      bool   `json:"published"`
	Pending        bool   `json:"pending,omitempty"`
	Superseded     bool   `json:"superseded,omitempty"`
	// LastMergeCommit is the merge already applied to the change. It makes
	// acceptance idempotent across retries and duplicate observations.
	LastMergeCommit string            `json:"last_merge_commit,omitempty"`
	Acceptance      *AcceptanceRecord `json:"acceptance,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type publicationState struct {
	Records       map[string]publicationRecord `json:"records"`
	Current       map[string]string            `json:"current"`
	ByPullRequest map[string]string            `json:"by_pull_request"`
}

// publicationAcceptanceEvent is one observed forge state.
type publicationAcceptanceEvent struct {
	Connector  string
	Repository string
	ChangeID   string
	RunID      string
	Result     forge.AcceptanceResult
	// HumanClosed reports that the close actor resolved to a person. The
	// store stays mechanical; the activity resolves the identity.
	HumanClosed bool
}

// publicationAcceptanceDecision names how an observation was applied.
type publicationAcceptanceDecision string

const (
	publicationDecisionPending        publicationAcceptanceDecision = "pending"
	publicationDecisionOpen           publicationAcceptanceDecision = "open"
	publicationDecisionAccepted       publicationAcceptanceDecision = "accepted"
	publicationDecisionAlreadyApplied publicationAcceptanceDecision = "already_applied"
	publicationDecisionDrift          publicationAcceptanceDecision = "drift"
	publicationDecisionClosedHuman    publicationAcceptanceDecision = "closed_human"
	publicationDecisionClosedService  publicationAcceptanceDecision = "closed_service"
	publicationDecisionIgnoredStale   publicationAcceptanceDecision = "ignored_stale"
)

type publicationAcceptanceOutcome struct {
	Decision publicationAcceptanceDecision
	Record   publicationRecord
	Note     string
}

type publicationStore struct {
	root string
}

func openPublicationStore(artifactRoot string) (*publicationStore, error) {
	if strings.TrimSpace(artifactRoot) == "" {
		return nil, errors.New("publication store: artifact root is required")
	}
	root := filepath.Join(artifactRoot, publicationDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("publication store: create %s: %w", root, err)
	}
	return &publicationStore{root: root}, nil
}

func (s *publicationStore) RecordPublication(rec publicationRecord) (publicationRecord, error) {
	if err := validatePublicationRecord(rec); err != nil {
		return publicationRecord{}, err
	}
	now := time.Now().UTC()
	var stored publicationRecord
	err := s.withState(func(state *publicationState) error {
		key := publicationRecordKey(rec.ChangeID, rec.RunID)
		prKey := publicationPRKey(rec.Connector, rec.Repository, rec.PullRequestID)
		if existingKey, ok := state.ByPullRequest[prKey]; ok && existingKey != key {
			existing := state.Records[existingKey]
			// Preserve an acceptance that arrived before publication wrote its
			// record. This is the repair path for a merge observed early.
			if rec.Acceptance == nil && existing.Acceptance != nil {
				rec.Acceptance = existing.Acceptance
				rec.LastMergeCommit = existing.LastMergeCommit
			}
			delete(state.Records, existingKey)
		}
		if currentKey, ok := state.Current[rec.ChangeID]; ok && currentKey != key {
			if current, ok := state.Records[currentKey]; ok {
				current.Superseded = true
				current.UpdatedAt = now
				state.Records[currentKey] = current
			}
		}
		if existing, ok := state.Records[key]; ok {
			if rec.Acceptance == nil {
				rec.Acceptance = existing.Acceptance
			}
			if rec.LastMergeCommit == "" {
				rec.LastMergeCommit = existing.LastMergeCommit
			}
			if rec.CreatedAt.IsZero() {
				rec.CreatedAt = existing.CreatedAt
			}
		}
		rec.Published = true
		rec.Pending = false
		rec.Superseded = false
		if rec.CreatedAt.IsZero() {
			rec.CreatedAt = now
		}
		rec.UpdatedAt = now
		if rec.Acceptance != nil && rec.Acceptance.ReadAt.IsZero() {
			rec.Acceptance.ReadAt = now
		}
		state.Records[key] = rec
		state.Current[rec.ChangeID] = key
		state.ByPullRequest[prKey] = key
		stored = rec
		return nil
	})
	return stored, err
}

// RecordAcceptance applies one forge observation.
//
// A missing publication record is stored as a pending record rather than
// discarded. When publication later links the same pull request, the
// observation is preserved and can be applied.
func (s *publicationStore) RecordAcceptance(ctx context.Context, event publicationAcceptanceEvent) (publicationAcceptanceOutcome, error) {
	if err := ctx.Err(); err != nil {
		return publicationAcceptanceOutcome{}, err
	}
	if !event.Result.State.Valid() {
		return publicationAcceptanceOutcome{}, fmt.Errorf("publication store: acceptance state %q is not supported", event.Result.State)
	}
	pullRequestID := strings.TrimSpace(event.Result.PullRequest.ID)
	if pullRequestID == "" {
		return publicationAcceptanceOutcome{}, errors.New("publication store: pull request id is required")
	}
	now := time.Now().UTC()
	var outcome publicationAcceptanceOutcome
	err := s.withState(func(state *publicationState) error {
		prKey := publicationPRKeyForEvent(event, pullRequestID)
		key, ok := state.ByPullRequest[prKey]
		if !ok {
			// A merge can arrive before the publication record. Store it under a
			// pending key; RecordPublication repairs it by PR identity.
			pendingKey := publicationPendingPrefix + prKey
			rec := publicationRecord{
				ChangeID:       strings.TrimSpace(event.ChangeID),
				RunID:          strings.TrimSpace(event.RunID),
				Connector:      strings.TrimSpace(event.Connector),
				Repository:     strings.TrimSpace(event.Repository),
				PullRequestID:  pullRequestID,
				PullRequestURL: strings.TrimSpace(event.Result.PullRequest.URL),
				Pending:        true,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			applyAcceptance(&rec, event, now)
			state.Records[pendingKey] = rec
			state.ByPullRequest[prKey] = pendingKey
			if rec.ChangeID != "" {
				state.Current[rec.ChangeID] = pendingKey
			}
			outcome = publicationAcceptanceOutcome{Decision: publicationDecisionPending, Record: rec, Note: "acceptance arrived before the publication record"}
			return nil
		}
		rec := state.Records[key]
		if rec.Superseded || (rec.ChangeID != "" && state.Current[rec.ChangeID] != key) {
			outcome = publicationAcceptanceOutcome{Decision: publicationDecisionIgnoredStale, Record: rec, Note: "pull request was superseded"}
			return nil
		}
		switch event.Result.State {
		case forge.AcceptanceOpen:
			applyAcceptance(&rec, event, now)
			state.Records[key] = rec
			outcome = publicationAcceptanceOutcome{Decision: publicationDecisionOpen, Record: rec}
		case forge.AcceptanceClosed:
			applyAcceptance(&rec, event, now)
			if event.HumanClosed {
				rec.Acceptance.Note = "closed without merging"
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionClosedHuman, Record: rec, Note: rec.Acceptance.Note}
			} else {
				rec.Acceptance.Note = "closed by an actor that could not be resolved as a human"
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionClosedService, Record: rec, Note: rec.Acceptance.Note}
			}
			state.Records[key] = rec
		case forge.AcceptanceMerged:
			mergeCommit := strings.TrimSpace(event.Result.MergeCommit)
			if mergeCommit != "" && rec.LastMergeCommit == mergeCommit {
				applyAcceptance(&rec, event, now)
				state.Records[key] = rec
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionAlreadyApplied, Record: rec}
				return nil
			}
			observedSource := strings.TrimSpace(event.Result.SourceCommit)
			storedCommit := strings.TrimSpace(rec.Commit)
			sourceMatches := observedSource != "" && storedCommit != "" && strings.EqualFold(storedCommit, observedSource)
			// Tree equality is only a fallback: it cannot distinguish a moved
			// base from a different change, and it is the only comparison an
			// older record or an older forge observation can support.
			treeFallback := observedSource == "" || storedCommit == ""
			treeMatches := strings.TrimSpace(event.Result.MergeTree) != "" &&
				strings.TrimSpace(rec.Tree) != "" &&
				strings.EqualFold(strings.TrimSpace(rec.Tree), strings.TrimSpace(event.Result.MergeTree))
			switch {
			case mergeCommit == "":
				applyAcceptance(&rec, event, now)
				rec.Acceptance.Note = "merged pull request did not report a merge commit"
				state.Records[key] = rec
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionDrift, Record: rec, Note: rec.Acceptance.Note}
			case sourceMatches:
				applyAcceptance(&rec, event, now)
				rec.Acceptance.Note = ""
				rec.LastMergeCommit = mergeCommit
				state.Records[key] = rec
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionAccepted, Record: rec}
			case strings.TrimSpace(event.Result.MergeTree) == "":
				applyAcceptance(&rec, event, now)
				rec.Acceptance.Note = "merged pull request did not report a merge tree"
				state.Records[key] = rec
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionDrift, Record: rec, Note: rec.Acceptance.Note}
			case treeFallback && treeMatches:
				applyAcceptance(&rec, event, now)
				rec.Acceptance.Note = ""
				rec.LastMergeCommit = mergeCommit
				state.Records[key] = rec
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionAccepted, Record: rec}
			case observedSource != "" && storedCommit != "":
				applyAcceptance(&rec, event, now)
				rec.Acceptance.Note = fmt.Sprintf("merged source commit %s does not match the published commit %s", observedSource, storedCommit)
				state.Records[key] = rec
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionDrift, Record: rec, Note: rec.Acceptance.Note}
			default:
				applyAcceptance(&rec, event, now)
				rec.Acceptance.Note = fmt.Sprintf("merged tree %s does not match the evidenced tree %s", event.Result.MergeTree, rec.Tree)
				state.Records[key] = rec
				outcome = publicationAcceptanceOutcome{Decision: publicationDecisionDrift, Record: rec, Note: rec.Acceptance.Note}
			}
		}
		return nil
	})
	return outcome, err
}

func applyAcceptance(rec *publicationRecord, event publicationAcceptanceEvent, now time.Time) {
	acceptance := AcceptanceRecord{
		State:             event.Result.State,
		MergeCommit:       strings.TrimSpace(event.Result.MergeCommit),
		MergeTree:         strings.TrimSpace(event.Result.MergeTree),
		SourceCommit:      strings.TrimSpace(event.Result.SourceCommit),
		Votes:             append([]forge.ApprovalVote(nil), event.Result.Votes...),
		AutoCompleteSetBy: strings.TrimSpace(event.Result.AutoCompleteSetBy),
		ClosedBy:          strings.TrimSpace(event.Result.ClosedBy),
		ReadAt:            now,
	}
	rec.Acceptance = &acceptance
	rec.UpdatedAt = now
}

func (s *publicationStore) Load(changeID string) (publicationRecord, bool, error) {
	state, err := s.load()
	if err != nil {
		return publicationRecord{}, false, err
	}
	key, ok := state.Current[strings.TrimSpace(changeID)]
	if !ok {
		return publicationRecord{}, false, nil
	}
	record, ok := state.Records[key]
	return record, ok, nil
}

func (s *publicationStore) LoadByPullRequest(connector, repository, pullRequestID string) (publicationRecord, bool, error) {
	state, err := s.load()
	if err != nil {
		return publicationRecord{}, false, err
	}
	key, ok := state.ByPullRequest[publicationPRKey(connector, repository, pullRequestID)]
	if !ok {
		return publicationRecord{}, false, nil
	}
	record, ok := state.Records[key]
	return record, ok, nil
}

func (s *publicationStore) load() (publicationState, error) {
	state := publicationState{
		Records:       map[string]publicationRecord{},
		Current:       map[string]string{},
		ByPullRequest: map[string]string{},
	}
	data, err := os.ReadFile(filepath.Join(s.root, publicationStateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("publication store: decode state: %w", err)
	}
	if state.Records == nil {
		state.Records = map[string]publicationRecord{}
	}
	if state.Current == nil {
		state.Current = map[string]string{}
	}
	if state.ByPullRequest == nil {
		state.ByPullRequest = map[string]string{}
	}
	return state, nil
}

func (s *publicationStore) withState(update func(*publicationState) error) error {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := root.OpenFile(publicationStateLockName, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	state, err := s.load()
	if err != nil {
		return err
	}
	if err := update(&state); err != nil {
		return err
	}
	return s.save(state)
}

func (s *publicationStore) save(state publicationState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("publication store: encode state: %w", err)
	}
	tmp, err := os.CreateTemp(s.root, ".publication-state-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(s.root, publicationStateFileName)); err != nil {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func validatePublicationRecord(rec publicationRecord) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"change id", rec.ChangeID},
		{"run id", rec.RunID},
		{"repository", rec.Repository},
		{"branch", rec.Branch},
		{"pull request id", rec.PullRequestID},
		{"commit", rec.Commit},
		{"tree", rec.Tree},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("publication store: %s is required", field.name)
		}
		if strings.ContainsAny(field.value, "\x00\r\n") {
			return fmt.Errorf("publication store: %s is invalid", field.name)
		}
	}
	return nil
}

func publicationRecordKey(changeID, runID string) string {
	return strings.TrimSpace(changeID) + "\x00" + strings.TrimSpace(runID)
}

func publicationPRKey(connector, repository, pullRequestID string) string {
	return strings.Join([]string{
		strings.TrimSpace(connector), strings.TrimSpace(repository), strings.TrimSpace(pullRequestID),
	}, "\x00")
}

func publicationPRKeyForEvent(event publicationAcceptanceEvent, pullRequestID string) string {
	return publicationPRKey(event.Connector, event.Repository, pullRequestID)
}
