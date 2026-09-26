package assurance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store serializes local transitions. The worker is the only writer; operators
// reach it through the authenticated socket, never through database permissions.
type Store struct {
	db  *sql.DB
	Now func() time.Time
}

func OpenStore(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Chmod(0600); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, Now: func() time.Time { return time.Now().UTC() }}
	for _, q := range []string{"PRAGMA busy_timeout=10000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 1 {
		db.Close()
		return nil, fmt.Errorf("unsupported quality schema %d", version)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS quality_runs(id TEXT PRIMARY KEY, body BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS quality_policies(name TEXT NOT NULL, version TEXT NOT NULL, digest TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(name,version));
CREATE TABLE IF NOT EXISTS quality_events(seq INTEGER PRIMARY KEY AUTOINCREMENT, aggregate_id TEXT NOT NULL, operation TEXT NOT NULL, input_digest TEXT NOT NULL, kind TEXT NOT NULL, at TEXT NOT NULL, data BLOB NOT NULL, UNIQUE(aggregate_id,operation));
CREATE TABLE IF NOT EXISTS quality_outbox(id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, aggregate_id TEXT NOT NULL, payload BLOB NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', done INTEGER NOT NULL DEFAULT 0, UNIQUE(kind,aggregate_id));
CREATE TABLE IF NOT EXISTS quality_objects(kind TEXT NOT NULL, id TEXT NOT NULL, body BLOB NOT NULL, PRIMARY KEY(kind,id));
CREATE TRIGGER IF NOT EXISTS quality_events_no_update BEFORE UPDATE ON quality_events BEGIN SELECT RAISE(ABORT,'audit events are immutable'); END;
CREATE TRIGGER IF NOT EXISTS quality_events_no_delete BEFORE DELETE ON quality_events BEGIN SELECT RAISE(ABORT,'audit events are immutable'); END;
CREATE TRIGGER IF NOT EXISTS quality_policy_no_update BEFORE UPDATE ON quality_policies BEGIN SELECT RAISE(ABORT,'policy versions are immutable'); END;
CREATE TRIGGER IF NOT EXISTS quality_policy_no_delete BEFORE DELETE ON quality_policies BEGIN SELECT RAISE(ABORT,'policy versions are immutable'); END;
PRAGMA user_version=1;`)
	if err != nil {
		tx.Rollback()
		db.Close()
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func NewMemoryStore() (*Store, error) { return OpenStore(":memory:") }
func (s *Store) Close() error         { return s.db.Close() }
func decodeRun(b []byte, err error) (Run, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	var r Run
	err = json.Unmarshal(b, &r)
	return r, err
}
func (s *Store) Get(ctx context.Context, id string) (Run, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT body FROM quality_runs WHERE id=?", id).Scan(&b)
	return decodeRun(b, err)
}
func (s *Store) List(ctx context.Context) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM quality_runs ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		r, err := decodeRun(b, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Reserve(ctx context.Context, r Run) (Run, error) {
	if !ValidID(r.ID) || r.AdmissionID == "" || r.RequestDigest == "" || r.Requester.ID == "" || r.Snapshot.Digest == "" {
		return Run{}, fmt.Errorf("incomplete quality admission")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRowContext(ctx, "SELECT body FROM quality_runs WHERE id=?", r.ID).Scan(&b)
	old, err := decodeRun(b, err)
	if err == nil {
		if old.RequestDigest != r.RequestDigest || old.Requester.ID != r.Requester.ID {
			return Run{}, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Run{}, err
	}
	for _, p := range r.Snapshot.Policies {
		body, _ := json.Marshal(p)
		digest := Digest(body)
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT digest FROM quality_policies WHERE name=? AND version=?", p.Metadata.Name, p.Metadata.Version).Scan(&existing)
		if err == nil && existing != digest {
			return Run{}, fmt.Errorf("%w: policy %s@%s", ErrConflict, p.Metadata.Name, p.Metadata.Version)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Run{}, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO quality_policies VALUES(?,?,?,?)", p.Metadata.Name, p.Metadata.Version, digest, body); err != nil {
			return Run{}, err
		}
	}
	r.CreatedAt = s.Now()
	r.Checkpoints = map[string]json.RawMessage{}
	b, err = json.Marshal(r)
	if err != nil {
		return Run{}, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO quality_runs VALUES(?,?)", r.ID, b); err != nil {
		return Run{}, err
	}
	if err = insertEvent(ctx, tx, r.ID, "admit", r.RequestDigest, "admitted", r.CreatedAt, b); err != nil {
		return Run{}, err
	}
	if err = enqueue(ctx, tx, "start-run", r.ID, json.RawMessage(r.Request)); err != nil {
		return Run{}, err
	}
	if err = tx.Commit(); err != nil {
		return Run{}, err
	}
	return r, nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, id, key, input, kind string, at time.Time, data []byte) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO quality_events(aggregate_id,operation,input_digest,kind,at,data) VALUES(?,?,?,?,?,?)", id, key, input, kind, at.Format(time.RFC3339Nano), data)
	return err
}
func enqueue(ctx context.Context, tx *sql.Tx, kind, id string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO quality_outbox(kind,aggregate_id,payload) VALUES(?,?,?)", kind, id, b)
	return err
}

// Transition commits a domain change and its audit receipt atomically. An
// operation key is reusable only with the identical input digest.
func (s *Store) Transition(ctx context.Context, id, key string, input any, fn func(*Run, time.Time) error) (Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRowContext(ctx, "SELECT body FROM quality_runs WHERE id=?", id).Scan(&b)
	r, err := decodeRun(b, err)
	if err != nil {
		return r, err
	}
	digest := Hash(input)
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT input_digest FROM quality_events WHERE aggregate_id=? AND operation=?", id, key).Scan(&previous)
	if err == nil {
		if previous != digest {
			return r, ErrConflict
		}
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if r.Decision != nil {
		return r, ErrTerminal
	}
	now := s.Now()
	if err = fn(&r, now); err != nil {
		return r, err
	}
	b, err = json.Marshal(r)
	if err != nil {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE quality_runs SET body=? WHERE id=?", b, id); err != nil {
		return r, err
	}
	eventData, _ := json.Marshal(input)
	if r.Decision != nil {
		eventData, _ = json.Marshal(map[string]any{"input": input, "decision": r.Decision, "manifest": r.Manifest, "attestation": r.Attestation, "attestation_digest": r.AttestationDigest})
	}
	if err = insertEvent(ctx, tx, id, key, digest, key, now, eventData); err != nil {
		return r, err
	}
	if r.Decision != nil {
		if err = enqueue(ctx, tx, "export-run", id, r.Manifest); err != nil {
			return r, err
		}
		if err = enqueue(ctx, tx, "publish-quality", id, r); err != nil {
			return r, err
		}
	}
	if r.Approval != nil && r.Approval.Status != "pending" {
		if err = enqueue(ctx, tx, "approval-wake", r.Approval.ID, map[string]string{"run_id": id}); err != nil {
			return r, err
		}
	}
	if err = tx.Commit(); err != nil {
		return r, err
	}
	return r, nil
}

// RegisterPolicies reserves versions at worker startup, before admission.
func (s *Store) RegisterPolicies(ctx context.Context, policies []Policy) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range policies {
		body, err := json.Marshal(p)
		if err != nil {
			return err
		}
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT digest FROM quality_policies WHERE name=? AND version=?", p.Metadata.Name, p.Metadata.Version).Scan(&existing)
		if err == nil && existing != Digest(body) {
			return fmt.Errorf("%w: policy %s@%s", ErrConflict, p.Metadata.Name, p.Metadata.Version)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO quality_policies VALUES(?,?,?,?)", p.Metadata.Name, p.Metadata.Version, Digest(body), body); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Events(ctx context.Context, id string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT seq,aggregate_id,operation,kind,at,data FROM quality_events WHERE aggregate_id=? ORDER BY seq", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at string
		if err = rows.Scan(&e.Sequence, &e.Aggregate, &e.Operation, &e.Type, &at, &e.Data); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Pending(ctx context.Context) ([]Outbox, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,kind,aggregate_id,payload,attempts,error FROM quality_outbox WHERE done=0 ORDER BY attempts,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Outbox{}
	for rows.Next() {
		var o Outbox
		if err = rows.Scan(&o.ID, &o.Kind, &o.Aggregate, &o.Payload, &o.Attempts, &o.Error); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) CompleteOutbox(ctx context.Context, id int64, problem string) error {
	done := 0
	if problem == "" {
		done = 1
	}
	_, err := s.db.ExecContext(ctx, "UPDATE quality_outbox SET attempts=attempts+1,error=?,done=? WHERE id=?", problem, done, id)
	return err
}

func (s *Store) Bind(ctx context.Context, id, admission, requestDigest, workflowID, executionID string) (Run, error) {
	return s.Transition(ctx, id, "bind", []string{admission, requestDigest, workflowID, executionID}, func(r *Run, _ time.Time) error {
		if r.AdmissionID != admission || r.RequestDigest != requestDigest || r.WorkflowID != workflowID || executionID == "" {
			return ErrForbidden
		}
		if r.ExecutionID != "" && r.ExecutionID != executionID {
			return ErrConflict
		}
		r.ExecutionID = executionID
		return nil
	})
}

func (s *Store) Checkpoint(ctx context.Context, id, key string, value any) (Run, error) {
	return s.Transition(ctx, id, "checkpoint/"+key, value, func(r *Run, _ time.Time) error {
		if r.Checkpoints == nil {
			r.Checkpoints = map[string]json.RawMessage{}
		}
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		r.Checkpoints[key] = b
		return nil
	})
}

// AuditFailure retains rejected service actions without permitting changes to
// historical decisions. The caller provides a unique request operation ID.
func (s *Store) AuditFailure(ctx context.Context, id, key string, actor Actor, reason string) error {
	b, _ := json.Marshal(map[string]any{"actor": actor, "reason": reason})
	_, err := s.db.ExecContext(ctx, "INSERT INTO quality_events(aggregate_id,operation,input_digest,kind,at,data) VALUES(?,?,?,?,?,?)", id, key, Digest(b), "action-denied", s.Now().Format(time.RFC3339Nano), b)
	return err
}
