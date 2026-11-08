package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	intakeDirName              = "intake"
	maxIntakeFieldBytes        = 4096
	intakeStateFileName        = "state.json"
	intakeAuditFileName        = "audit.jsonl"
	intakeStateLockFileName    = "state.lock"
	intakeRecordAccepted       = "accepted"
	intakeRecordSucceeded      = "succeeded"
	intakeRecordFailed         = "failed"
	intakeRecordWithdrawn      = "withdrawn"
	intakeRecordError          = "error"
	intakeStatusDuplicate      = "duplicate"
	intakeStatusStale          = "stale"
	intakeStatusConflict       = "snapshot_conflict"
	intakeStatusNoMapping      = "no_mapping"
	intakeStatusScopeAmbiguous = "scope_ambiguous"
	intakeStatusRejected       = "rejected"
	intakeStatusRunning        = "running"
)

// intakeRecord is one durable intake decision. Its identity is the mapped
// repository plus work-item revision, so a webhook and a sweep converge.
type intakeRecord struct {
	Connector           string    `json:"connector"`
	Pipeline            string    `json:"pipeline,omitempty"`
	Repository          string    `json:"repository"`
	WorkItemID          string    `json:"work_item_id"`
	Revision            string    `json:"revision"`
	SnapshotHash        string    `json:"snapshot_hash"`
	Source              string    `json:"source,omitempty"`
	DeliveryID          string    `json:"delivery_id,omitempty"`
	ActorDescriptor     string    `json:"actor_descriptor,omitempty"`
	ActorPermission     string    `json:"actor_permission,omitempty"`
	ChangeID            string    `json:"change_id"`
	RunID               string    `json:"run_id"`
	ActivationID        string    `json:"activation_id"`
	ParentRunID         string    `json:"parent_run_id,omitempty"`
	BaseBranch          string    `json:"base_branch,omitempty"`
	Scope               string    `json:"scope,omitempty"`
	VerificationProfile string    `json:"verification_profile,omitempty"`
	Status              string    `json:"status"`
	FactoryResult       string    `json:"factory_result,omitempty"`
	Error               string    `json:"error,omitempty"`
	RecordedAt          time.Time `json:"recorded_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type intakeHead struct {
	Connector      string    `json:"connector"`
	Repository     string    `json:"repository"`
	WorkItemID     string    `json:"work_item_id"`
	LatestRevision string    `json:"latest_revision"`
	SnapshotHash   string    `json:"snapshot_hash"`
	ChangeID       string    `json:"change_id"`
	RunID          string    `json:"run_id"`
	ActivationID   string    `json:"activation_id"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type intakeState struct {
	Heads   map[string]intakeHead   `json:"heads"`
	Records map[string]intakeRecord `json:"records"`
}

type intakeClaim struct {
	Accepted bool
	Status   string
	Reason   string
	Record   intakeRecord
}

type intakeStore struct {
	root  string
	state lockedState[intakeState]
}

func openIntakeStore(artifactRoot string) (*intakeStore, error) {
	if strings.TrimSpace(artifactRoot) == "" {
		return nil, errors.New("intake store: artifact root is required")
	}
	root := filepath.Join(artifactRoot, intakeDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("intake store: create %s: %w", root, err)
	}
	return &intakeStore{root: root, state: lockedState[intakeState]{
		root:        root,
		fileName:    intakeStateFileName,
		lockName:    intakeStateLockFileName,
		tempPattern: ".intake-state-*",
		label:       "intake store",
		newState: func() intakeState {
			return intakeState{
				Heads:   map[string]intakeHead{},
				Records: map[string]intakeRecord{},
			}
		},
		normalize: func(state *intakeState) {
			if state.Heads == nil {
				state.Heads = map[string]intakeHead{}
			}
			if state.Records == nil {
				state.Records = map[string]intakeRecord{}
			}
		},
	}}, nil
}

func (s *intakeStore) Claim(ctx context.Context, rec intakeRecord) (intakeClaim, error) {
	if err := ctx.Err(); err != nil {
		return intakeClaim{}, err
	}
	if err := validateIntakeRecord(rec); err != nil {
		return intakeClaim{}, err
	}
	now := time.Now().UTC()
	rec.RecordedAt = now
	rec.UpdatedAt = now
	rec.Status = intakeRecordAccepted
	rec.ActorDescriptor = strings.TrimSpace(rec.ActorDescriptor)
	rec.Scope = strings.TrimSpace(rec.Scope)

	var claim intakeClaim
	err := s.state.withState(func(state *intakeState) error {
		recordKey := intakeRecordKey(rec)
		headKey := intakeHeadKey(rec)
		head, hasHead := state.Heads[headKey]
		if existing, ok := state.Records[recordKey]; ok {
			claim.Record = existing
			if existing.SnapshotHash != rec.SnapshotHash {
				claim.Status = intakeStatusConflict
				claim.Reason = "the work item snapshot changed without a revision change"
				return nil
			}
			// The same delivery retried after a crash between the durable write
			// and the activity acknowledgement must resume, not silently lose the
			// activation. A different delivery identity is a duplicate. A record
			// that already has an outcome is a duplicate in every case.
			if existing.Status == intakeRecordAccepted &&
				existing.Source == rec.Source &&
				existing.DeliveryID == rec.DeliveryID &&
				existing.ActivationID == rec.ActivationID {
				claim.Accepted = true
				claim.Status = intakeRecordAccepted
				return nil
			}
			claim.Status = intakeStatusDuplicate
			claim.Reason = "the work item revision was already recorded"
			return nil
		}
		if hasHead {
			switch compareWorkItemRevisions(rec.Revision, head.LatestRevision) {
			case -1:
				claim.Status = intakeStatusStale
				claim.Reason = "a newer work item revision was already processed"
				claim.Record = state.Records[intakeRecordKey(intakeRecord{
					Connector: rec.Connector, Repository: head.Repository, WorkItemID: head.WorkItemID, Revision: head.LatestRevision,
				})]
				return nil
			case 0:
				claim.Status = intakeStatusDuplicate
				claim.Reason = "the work item revision was already recorded"
				return nil
			}
			if rec.ParentRunID == "" {
				rec.ParentRunID = head.RunID
			}
		}
		state.Records[recordKey] = rec
		state.Heads[headKey] = intakeHead{
			Connector:      rec.Connector,
			Repository:     rec.Repository,
			WorkItemID:     rec.WorkItemID,
			LatestRevision: rec.Revision,
			SnapshotHash:   rec.SnapshotHash,
			ChangeID:       rec.ChangeID,
			RunID:          rec.RunID,
			ActivationID:   rec.ActivationID,
			UpdatedAt:      now,
		}
		claim.Accepted = true
		claim.Status = intakeRecordAccepted
		claim.Record = rec
		return nil
	})
	return claim, err
}

func (s *intakeStore) RecordOutcome(ctx context.Context, in WorkItemOutcomeInput) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.state.withState(func(state *intakeState) error {
		rec := intakeRecord{
			Connector: in.Connector, Repository: in.Repository,
			WorkItemID: in.WorkItemID, Revision: in.Revision,
		}
		recordKey := intakeRecordKey(rec)
		record, ok := state.Records[recordKey]
		if !ok {
			return fmt.Errorf("intake store: no record for %s revision %s", in.WorkItemID, in.Revision)
		}
		if in.Status == intakeStatusDuplicate && record.Status != intakeRecordAccepted {
			// A late duplicate-start report must not erase a completed outcome.
			return nil
		}
		record.Status = in.Status
		record.FactoryResult = in.FactoryResult
		record.Error = in.Error
		record.UpdatedAt = time.Now().UTC()
		state.Records[recordKey] = record
		headKey := intakeHeadKey(rec)
		if head, ok := state.Heads[headKey]; ok && head.LatestRevision == in.Revision {
			head.UpdatedAt = record.UpdatedAt
			state.Heads[headKey] = head
		}
		return nil
	})
}

// QueueDepth counts intake records that have not reached a terminal state,
// grouped by the resolved operator scope.
//
// Every scope observed in the records is present, including one with no pending
// records at zero, so a gauge can return to zero. The result is never nil.
// Records written before the scope was persisted carry none and contribute no
// series rather than a wrong one.
func (s *intakeStore) QueueDepth() (map[string]int, error) {
	state, err := s.Load()
	if err != nil {
		return nil, err
	}
	depths := make(map[string]int)
	for _, record := range state.Records {
		scope := strings.TrimSpace(record.Scope)
		if scope == "" {
			continue
		}
		if _, ok := depths[scope]; !ok {
			depths[scope] = 0
		}
		if intakeRecordTerminal(record.Status) {
			continue
		}
		depths[scope]++
	}
	return depths, nil
}

// intakeRecordTerminal reports whether an intake record has an outcome. Accepted
// work has none yet and is still queued.
func intakeRecordTerminal(status string) bool {
	switch status {
	case intakeRecordSucceeded, intakeRecordFailed, intakeRecordWithdrawn, intakeRecordError, intakeStatusDuplicate:
		return true
	default:
		return false
	}
}

func (s *intakeStore) Load() (intakeState, error) {
	return s.state.load()
}

func validateIntakeRecord(rec intakeRecord) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"connector", rec.Connector},
		{"repository", rec.Repository},
		{"work item id", rec.WorkItemID},
		{"revision", rec.Revision},
		{"snapshot digest", rec.SnapshotHash},
		{"change id", rec.ChangeID},
		{"run id", rec.RunID},
		{"activation id", rec.ActivationID},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("intake store: %s is required", field.name)
		}
		if len(field.value) > maxIntakeFieldBytes || strings.ContainsAny(field.value, "\x00\r\n\t") {
			return fmt.Errorf("intake store: %s is invalid", field.name)
		}
	}
	if scope := strings.TrimSpace(rec.Scope); scope != "" {
		if len(scope) > maxIntakeFieldBytes || strings.ContainsAny(scope, "\x00\r\n\t") {
			return fmt.Errorf("intake store: scope is invalid")
		}
	}
	return nil
}

func intakeRecordKey(rec intakeRecord) string {
	return digestText(strings.Join([]string{
		rec.Connector, rec.Repository, rec.WorkItemID, rec.Revision,
	}, "\x00"), 0)
}

func intakeHeadKey(rec intakeRecord) string {
	return digestText(strings.Join([]string{
		rec.Connector, rec.Repository, rec.WorkItemID,
	}, "\x00"), 0)
}

// appendIntakeAudit appends one durable intake decision.
//
// Intake rejections have no run of their own, so they cannot use a run's audit
// artifact. The intake audit is the record that a duplicate or stale delivery
// was seen and did not start work.
func appendIntakeAudit(store *intakeStore, entry AuditEntry, redactor *Redactor) error {
	entry = redactAuditEntry(entry, redactor)
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	if entry.Action == "" {
		entry.Action = AuditIntake
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	path := filepath.Join(store.root, intakeAuditFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
