package assurance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// QMSProvider is a vendor boundary. Every mutation carries a stable operation
// key. Providers must declare unsupported operations rather than invent success.
type QMSProvider interface {
	GetRequirement(context.Context, string) (json.RawMessage, error)
	GetChangeRecord(context.Context, string) (json.RawMessage, error)
	CreateQualityRecord(context.Context, string, Run) error
	PublishEvidence(context.Context, string, Evidence) error
	CreateNonConformance(context.Context, string, NonConformance) error
	CreateCAPA(context.Context, string, CAPA) error
	UpdateCAPA(context.Context, string, CAPA) error
	RequestApproval(context.Context, string, ApprovalRequest) error
	GetApprovalStatus(context.Context, string) (ApprovalRequest, error)
	PublishReleaseRecord(context.Context, string, json.RawMessage) error
}

// LocalProvider projects enterprise-style records onto the same authoritative
// store. It does not own a second quality decision engine.
type LocalProvider struct{ Store *Store }

func (p LocalProvider) GetRequirement(ctx context.Context, id string) (json.RawMessage, error) {
	return p.Store.Object(ctx, "requirement", id)
}
func (p LocalProvider) GetChangeRecord(ctx context.Context, id string) (json.RawMessage, error) {
	return p.Store.Object(ctx, "change", id)
}
func (p LocalProvider) CreateQualityRecord(ctx context.Context, key string, r Run) error {
	return p.Store.Import(ctx, "quality-record", key, r)
}
func (p LocalProvider) PublishEvidence(ctx context.Context, key string, e Evidence) error {
	return p.Store.Import(ctx, "published-evidence", key, e)
}
func (p LocalProvider) CreateNonConformance(ctx context.Context, key string, n NonConformance) error {
	return p.Store.Import(ctx, "published-nc", key, n)
}
func (p LocalProvider) CreateCAPA(ctx context.Context, key string, c CAPA) error {
	return p.Store.Import(ctx, "published-capa", key, c)
}
func (p LocalProvider) UpdateCAPA(ctx context.Context, key string, c CAPA) error {
	return p.Store.Import(ctx, "published-capa-transition", key, c)
}
func (p LocalProvider) RequestApproval(ctx context.Context, key string, a ApprovalRequest) error {
	return p.Store.Import(ctx, "published-approval", key, a)
}
func (p LocalProvider) GetApprovalStatus(ctx context.Context, id string) (ApprovalRequest, error) {
	r, err := p.Store.Get(ctx, id)
	if err != nil {
		return ApprovalRequest{}, err
	}
	if r.Approval == nil {
		return ApprovalRequest{}, ErrNotFound
	}
	return *r.Approval, nil
}
func (p LocalProvider) PublishReleaseRecord(context.Context, string, json.RawMessage) error {
	return fmt.Errorf("release authorization is not implemented; patch readiness is not release authority")
}

func (s *Store) Object(ctx context.Context, kind, id string) (json.RawMessage, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT body FROM quality_objects WHERE kind=? AND id=?", kind, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}
func (s *Store) Objects(ctx context.Context, kind string) ([]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT body FROM quality_objects WHERE kind=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}

// Import pins remote facts and local provider receipts under immutable keys.
func (s *Store) Import(ctx context.Context, kind, key string, v any) error {
	return s.ImportAs(ctx, kind, key, v, Actor{ID: "factory", Kind: "factory"})
}

func (s *Store) ImportAs(ctx context.Context, kind, key string, v any, actor Actor) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old []byte
	err = tx.QueryRowContext(ctx, "SELECT body FROM quality_objects WHERE kind=? AND id=?", kind, key).Scan(&old)
	if err == nil {
		if Digest(old) != Digest(b) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO quality_objects VALUES(?,?,?)", kind, key, b); err != nil {
		return err
	}
	event, _ := json.Marshal(map[string]any{"actor": actor, "fact": json.RawMessage(b)})
	if err = insertEvent(ctx, tx, kind+"/"+key, "import", Digest(b), "fact-imported", s.Now(), event); err != nil {
		return err
	}
	if kind == "acknowledgment" {
		var wake struct {
			RunID string `json:"run_id"`
		}
		if json.Unmarshal(b, &wake) == nil && ValidID(wake.RunID) {
			if err = enqueue(ctx, tx, "provider-wake", key, map[string]string{"run_id": wake.RunID}); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ImportedAt(ctx context.Context, kind, key string) (time.Time, error) {
	var at string
	err := s.db.QueryRowContext(ctx, "SELECT at FROM quality_events WHERE aggregate_id=? AND operation='import'", kind+"/"+key).Scan(&at)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, at)
}

type CAPA struct {
	ID                    string     `json:"id"`
	RunID                 string     `json:"run_id"`
	NonConformanceID      string     `json:"non_conformance_id"`
	Status                string     `json:"status"`
	RootCause             string     `json:"root_cause"`
	AcceptanceCriteria    string     `json:"acceptance_criteria"`
	CorrectiveActions     string     `json:"corrective_actions"`
	PreventiveActions     string     `json:"preventive_actions"`
	CorrectiveRuns        []string   `json:"corrective_runs"`
	PreventiveRuns        []string   `json:"preventive_runs"`
	VerificationEvidence  []Evidence `json:"verification_evidence"`
	CreatedBy             Actor      `json:"created_by"`
	PlanAuthoredBy        *Actor     `json:"plan_authored_by,omitempty"`
	CriteriaDigest        string     `json:"criteria_digest,omitempty"`
	VerificationRationale string     `json:"verification_rationale,omitempty"`
	PlanApprovedBy        *Actor     `json:"plan_approved_by,omitempty"`
	VerifiedBy            *Actor     `json:"verified_by,omitempty"`
	ClosedBy              *Actor     `json:"closed_by,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	ClosedAt              *time.Time `json:"closed_at,omitempty"`
}
type CAPAAction struct {
	Operation          string   `json:"operation"`
	Action             string   `json:"action"`
	Reason             string   `json:"reason"`
	RootCause          string   `json:"root_cause,omitempty"`
	AcceptanceCriteria string   `json:"acceptance_criteria,omitempty"`
	CorrectiveActions  string   `json:"corrective_actions,omitempty"`
	PreventiveActions  string   `json:"preventive_actions,omitempty"`
	RunID              string   `json:"run_id,omitempty"`
	CriteriaDigest     string   `json:"criteria_digest,omitempty"`
	EvidenceDigests    []string `json:"evidence_digests,omitempty"`
}

func (s *Store) GetCAPA(ctx context.Context, id string) (CAPA, error) {
	b, err := s.Object(ctx, "capa", id)
	if err != nil {
		return CAPA{}, err
	}
	var c CAPA
	err = json.Unmarshal(b, &c)
	return c, err
}
func (s *Store) OpenCAPA(ctx context.Context, id, runID, ncID string, actor Actor) (CAPA, error) {
	if !ValidID(id) || actor.Kind != "human" {
		return CAPA{}, fmt.Errorf("invalid CAPA admission")
	}
	r, err := s.Get(ctx, runID)
	if err != nil {
		return CAPA{}, err
	}
	found := false
	for _, nc := range r.NonConformances {
		if nc.ID == ncID {
			found = true
		}
	}
	if !found {
		return CAPA{}, fmt.Errorf("non-conformance does not exist")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CAPA{}, err
	}
	defer tx.Rollback()
	var old []byte
	err = tx.QueryRowContext(ctx, "SELECT body FROM quality_objects WHERE kind='capa' AND id=?", id).Scan(&old)
	if err == nil {
		var c CAPA
		if err = json.Unmarshal(old, &c); err != nil {
			return c, err
		}
		if c.RunID != runID || c.NonConformanceID != ncID || c.CreatedBy.ID != actor.ID {
			return c, ErrConflict
		}
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CAPA{}, err
	}
	now := s.Now()
	c := CAPA{ID: id, RunID: runID, NonConformanceID: ncID, Status: "root-cause", CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	b, _ := json.Marshal(c)
	if _, err = tx.ExecContext(ctx, "INSERT INTO quality_objects VALUES('capa',?,?)", id, b); err != nil {
		return c, err
	}
	if err = insertEvent(ctx, tx, "capa/"+id, "open", Digest(b), "capa-opened", now, b); err != nil {
		return c, err
	}
	if err = enqueue(ctx, tx, "start-capa", id, c); err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func (s *Store) AdvanceCAPA(ctx context.Context, id string, actor Actor, in CAPAAction) (CAPA, error) {
	if !ValidID(in.Operation) || in.Reason == "" || actor.Kind != "human" {
		return CAPA{}, fmt.Errorf("CAPA action needs operation, reason and human actor")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CAPA{}, err
	}
	defer tx.Rollback()
	var b []byte
	err = tx.QueryRowContext(ctx, "SELECT body FROM quality_objects WHERE kind='capa' AND id=?", id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return CAPA{}, ErrNotFound
	}
	if err != nil {
		return CAPA{}, err
	}
	var c CAPA
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	data, _ := json.Marshal(struct {
		Actor Actor
		Input CAPAAction
	}{actor, in})
	digest := Digest(data)
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT input_digest FROM quality_events WHERE aggregate_id=? AND operation=?", "capa/"+id, in.Operation).Scan(&previous)
	if err == nil {
		if previous != digest {
			return c, ErrConflict
		}
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	if c.Status == "closed" {
		return c, ErrTerminal
	}
	now := s.Now()
	switch in.Action {
	case "plan":
		if c.Status != "root-cause" || in.RootCause == "" || in.AcceptanceCriteria == "" || in.CorrectiveActions == "" || in.PreventiveActions == "" {
			return c, fmt.Errorf("complete root cause, criteria, corrective and preventive actions required")
		}
		c.RootCause = in.RootCause
		c.AcceptanceCriteria = in.AcceptanceCriteria
		c.CorrectiveActions = in.CorrectiveActions
		c.PreventiveActions = in.PreventiveActions
		c.PlanAuthoredBy = &actor
		c.CriteriaDigest = Digest([]byte(in.AcceptanceCriteria))
		c.Status = "plan-approval"
	case "approve-plan":
		if c.Status != "plan-approval" {
			return c, ErrConflict
		}
		if actor.ID == c.CreatedBy.ID || (c.PlanAuthoredBy != nil && actor.ID == c.PlanAuthoredBy.ID) {
			return c, ErrForbidden
		}
		c.PlanApprovedBy = &actor
		c.Status = "actions"
	case "link-corrective", "link-preventive":
		if c.Status != "actions" || in.RunID == c.RunID {
			return c, ErrConflict
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, "SELECT body FROM quality_runs WHERE id=?", in.RunID).Scan(&raw); err != nil {
			return c, err
		}
		var r Run
		if err = json.Unmarshal(raw, &r); err != nil {
			return c, err
		}
		if in.Action == "link-corrective" {
			if !Contains(c.CorrectiveRuns, in.RunID) {
				c.CorrectiveRuns = append(c.CorrectiveRuns, in.RunID)
			}
		} else {
			if !Contains(c.PreventiveRuns, in.RunID) {
				c.PreventiveRuns = append(c.PreventiveRuns, in.RunID)
			}
		}
	case "verify":
		if in.CriteriaDigest != c.CriteriaDigest || len(in.EvidenceDigests) == 0 {
			return c, fmt.Errorf("verification must bind the approved criteria digest and selected effectiveness evidence")
		}
		if c.Status != "actions" || len(c.CorrectiveRuns) == 0 || len(c.PreventiveRuns) == 0 {
			return c, fmt.Errorf("corrective and preventive runs are required")
		}
		if actor.ID == c.CreatedBy.ID || c.PlanApprovedBy != nil && actor.ID == c.PlanApprovedBy.ID || c.PlanAuthoredBy != nil && actor.ID == c.PlanAuthoredBy.ID {
			return c, ErrForbidden
		}
		selected := map[string]bool{}
		for _, digest := range in.EvidenceDigests {
			selected[digest] = false
		}
		for _, runID := range append(append([]string{}, c.CorrectiveRuns...), c.PreventiveRuns...) {
			var raw []byte
			if err = tx.QueryRowContext(ctx, "SELECT body FROM quality_runs WHERE id=?", runID).Scan(&raw); err != nil {
				return c, err
			}
			var r Run
			if err = json.Unmarshal(raw, &r); err != nil {
				return c, err
			}
			if r.Decision == nil || r.Decision.Result != "approved" || r.Requester.ID == actor.ID {
				return c, fmt.Errorf("remediation must be independently approved")
			}
			for _, e := range r.Evidence {
				if _, ok := selected[e.Digest]; ok && !e.Redacted {
					selected[e.Digest] = true
					c.VerificationEvidence = append(c.VerificationEvidence, e)
				}
			}
		}
		for _, found := range selected {
			if !found {
				return c, fmt.Errorf("effectiveness evidence must belong to approved remediation runs")
			}
		}
		c.VerificationRationale = in.Reason
		c.VerifiedBy = &actor
		c.Status = "closure"
	case "close":
		if c.Status != "closure" || c.VerifiedBy == nil || actor.ID == c.CreatedBy.ID || actor.ID == c.VerifiedBy.ID {
			return c, ErrForbidden
		}
		c.ClosedBy = &actor
		c.ClosedAt = &now
		c.Status = "closed"
	default:
		return c, fmt.Errorf("unknown CAPA action")
	}
	c.UpdatedAt = now
	b, _ = json.Marshal(c)
	if _, err = tx.ExecContext(ctx, "UPDATE quality_objects SET body=? WHERE kind='capa' AND id=?", b, id); err != nil {
		return c, err
	}
	if err = insertEvent(ctx, tx, "capa/"+id, in.Operation, digest, "capa-"+in.Action, now, data); err != nil {
		return c, err
	}
	return c, tx.Commit()
}
