package assurance

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Store) OpenApproval(ctx context.Context, id string) (Run, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Plan == nil || r.Candidate == nil {
		return r, fmt.Errorf("candidate and plan required")
	}
	return s.Transition(ctx, id, "approval-request", []string{r.Plan.Digest, r.Candidate.Digest}, func(r *Run, now time.Time) error {
		missing := MissingControls(*r)
		for _, m := range missing {
			permitted := false
			for _, rule := range r.Plan.Exceptions {
				if strings.HasPrefix(m, "control:") && Contains(rule.Controls, strings.TrimPrefix(m, "control:")) {
					permitted = true
				}
			}
			if !permitted || !waivableFailure(*r, strings.TrimPrefix(m, "control:")) {
				return fmt.Errorf("controls incomplete: %v", missing)
			}
		}
		var rules []ApprovalRule
		timeout := 30 * 24 * time.Hour
		for _, rule := range r.Plan.Approvals {
			if rule.Mode == "human" {
				rules = append(rules, rule)
				d, err := time.ParseDuration(rule.Timeout)
				if err != nil {
					return err
				}
				if d < timeout {
					timeout = d
				}
			}
		}
		status := "pending"
		if len(rules) == 0 && len(missing) == 0 {
			status = "approved"
		}
		if len(missing) > 0 && len(rules) == 0 && timeout > time.Hour {
			timeout = time.Hour
		}
		r.Approval = &ApprovalRequest{ID: r.ID + "-approval", CandidateDigest: r.Candidate.Digest, PlanDigest: r.Plan.Digest, Rules: rules, Deadline: now.Add(timeout), RequestedAt: now, Status: status}
		if len(missing) > 0 {
			disposition := now.Add(time.Hour)
			if r.Approval.Deadline.Before(disposition) {
				disposition = r.Approval.Deadline
			}
			r.Approval.DispositionDeadline = &disposition
		}
		return nil
	})
}

type ApprovalInput struct {
	Operation       string `json:"operation"`
	RequestID       string `json:"request_id"`
	CandidateDigest string `json:"candidate_digest"`
	PlanDigest      string `json:"plan_digest"`
	Decision        string `json:"decision"`
	Reason          string `json:"reason"`
}

func (s *Store) Approve(ctx context.Context, id string, actor Actor, in ApprovalInput) (Run, error) {
	if !ValidID(in.Operation) || actor.Kind != "human" || in.Reason == "" || (in.Decision != "approve" && in.Decision != "reject") {
		return Run{}, fmt.Errorf("invalid approval decision")
	}
	return s.Transition(ctx, id, "approval/"+in.Operation, struct {
		Actor Actor
		Input ApprovalInput
	}{actor, in}, func(r *Run, now time.Time) error {
		a := r.Approval
		if a == nil || a.ID != in.RequestID || a.CandidateDigest != in.CandidateDigest || a.PlanDigest != in.PlanDigest {
			return ErrConflict
		}
		if a.Status != "pending" {
			return ErrTerminal
		}
		if in.Decision == "approve" && len(MissingControls(*r)) > 0 {
			return fmt.Errorf("required controls remain unsatisfied")
		}
		if !now.Before(ApprovalDeadline(*r)) {
			return fmt.Errorf("approval deadline expired")
		}
		eligible := false
		for _, rule := range a.Rules {
			if HasRole(actor, rule.Roles) && (!rule.Independent || actor.ID != r.Requester.ID) {
				eligible = true
			}
		}
		if !eligible {
			return ErrForbidden
		}
		for _, d := range a.Decisions {
			if d.Actor.ID == actor.ID {
				return fmt.Errorf("actor already decided")
			}
		}
		a.Decisions = append(a.Decisions, ApprovalDecision{Actor: actor, Decision: in.Decision, Reason: in.Reason, At: now})
		if in.Decision == "reject" {
			a.Status = "rejected"
			return nil
		}
		if approvalSatisfied(*a, r.Requester.ID) {
			a.Status = "approved"
		}
		return nil
	})
}

// Distinct principals must fill each required slot; one multi-role operator
// cannot satisfy several separation-of-duties obligations by counting twice.
func approvalSatisfied(a ApprovalRequest, requester string) bool {
	var slots []ApprovalRule
	for _, rule := range a.Rules {
		for i := 0; i < rule.Count; i++ {
			slots = append(slots, rule)
		}
	}
	used := map[string]bool{}
	var assign func(int) bool
	assign = func(i int) bool {
		if i == len(slots) {
			return true
		}
		rule := slots[i]
		for _, d := range a.Decisions {
			if d.Decision != "approve" || used[d.Actor.ID] || !HasRole(d.Actor, rule.Roles) || (rule.Independent && d.Actor.ID == requester) {
				continue
			}
			used[d.Actor.ID] = true
			if assign(i + 1) {
				return true
			}
			delete(used, d.Actor.ID)
		}
		return false
	}
	return assign(0)
}

func (s *Store) ResolveApproval(ctx context.Context, id string) (Run, error) {
	r, err := s.Get(ctx, id)
	if err != nil || r.Approval == nil || r.Approval.Status != "pending" {
		return r, err
	}
	deadline := ApprovalDeadline(r)
	if s.Now().Before(deadline) {
		return r, nil
	}
	return s.Transition(ctx, id, "approval-expired/"+deadline.Format(time.RFC3339Nano), r.Approval.ID, func(r *Run, now time.Time) error {
		if r.Approval.Status == "pending" && !now.Before(ApprovalDeadline(*r)) {
			r.Approval.Status = "expired"
		}
		return nil
	})
}

// ApprovalDeadline preserves the original human budget after disposition.
func ApprovalDeadline(r Run) time.Time {
	if r.Approval == nil {
		return time.Time{}
	}
	if r.Approval.DispositionDeadline != nil && len(MissingControls(r)) > 0 {
		return *r.Approval.DispositionDeadline
	}
	return r.Approval.Deadline
}

type ExceptionInput struct {
	Operation       string `json:"operation"`
	ControlID       string `json:"control_id"`
	CandidateDigest string `json:"candidate_digest"`
	PlanDigest      string `json:"plan_digest"`
	Reason          string `json:"reason"`
}

func (s *Store) GrantException(ctx context.Context, id string, actor Actor, in ExceptionInput) (Run, error) {
	if !ValidID(in.Operation) || actor.Kind != "human" || in.Reason == "" {
		return Run{}, fmt.Errorf("exception requires human actor, operation, and reason")
	}
	return s.Transition(ctx, id, "exception/"+in.Operation, struct {
		Actor Actor
		Input ExceptionInput
	}{actor, in}, func(r *Run, now time.Time) error {
		if r.Plan == nil || r.Candidate == nil || r.Plan.Digest != in.PlanDigest || r.Candidate.Digest != in.CandidateDigest {
			return ErrConflict
		}
		if r.Approval == nil || r.Approval.Status != "pending" || !now.Before(ApprovalDeadline(*r)) {
			return fmt.Errorf("exception requires an unexpired pending decision request")
		}
		if actor.ID == r.Requester.ID {
			return ErrForbidden
		}
		permitted := false
		for _, rule := range r.Plan.Exceptions {
			if Contains(rule.Controls, in.ControlID) && HasRole(actor, rule.Roles) {
				permitted = true
			}
		}
		if !permitted {
			return ErrForbidden
		}
		if !waivableFailure(*r, in.ControlID) {
			return fmt.Errorf("exception requires a completed functional failure; provenance and integrity failures cannot be waived")
		}
		for _, e := range r.Exceptions {
			if e.ControlID == in.ControlID {
				return ErrConflict
			}
		}
		r.Exceptions = append(r.Exceptions, Exception{in.ControlID, in.CandidateDigest, in.PlanDigest, actor, in.Reason, now})
		for i := range r.NonConformances {
			if r.NonConformances[i].ControlID == in.ControlID {
				r.NonConformances[i].Disposition = "exception: " + in.Reason
				r.NonConformances[i].Status = "accepted-exception"
			}
		}
		if len(MissingControls(*r)) == 0 && approvalSatisfied(*r.Approval, r.Requester.ID) {
			r.Approval.Status = "approved"
		}
		return nil
	})
}

func waivableFailure(r Run, control string) bool {
	found := false
	for _, g := range r.Gates {
		if g.ControlID != control {
			continue
		}
		if g.NonWaivable || g.Status == "error" && !g.Retryable {
			return false
		}
		if g.Status == "failed" {
			found = true
		}
	}
	return found
}

func MissingControls(r Run) []string {
	if r.Plan == nil || r.Candidate == nil {
		return []string{"candidate-or-plan-missing"}
	}
	missing := []string{}
	waived := map[string]bool{}
	for _, e := range r.Exceptions {
		if e.CandidateDigest == r.Candidate.Digest && e.PlanDigest == r.Plan.Digest {
			waived[e.ControlID] = true
		}
	}
	for _, c := range r.Plan.Controls {
		if c.Optional || waived[c.ID] {
			continue
		}
		passed := false
		failed := false
		for _, g := range r.Gates {
			if g.ControlID != c.ID || g.CandidateDigest != r.Candidate.Digest || g.PlanDigest != r.Plan.Digest {
				continue
			}
			if g.Status == "passed" {
				passed = true
			}
			if g.Status == "failed" || (g.Status == "error" && !g.Retryable) {
				failed = true
			}
		}
		if !passed || failed {
			missing = append(missing, "control:"+c.ID)
		}
	}
	for _, kind := range r.Plan.Evidence {
		found := false
		for _, e := range r.Evidence {
			if e.Kind == kind && !e.Redacted && (e.CandidateDigest == "" || e.CandidateDigest == r.Candidate.Digest) {
				found = true
			}
		}
		if !found {
			missing = append(missing, "evidence:"+kind)
		}
	}
	sort.Strings(missing)
	return missing
}

func (s *Store) RecordGate(ctx context.Context, id string, g GateAttempt) (Run, error) {
	return s.Transition(ctx, id, "gate/"+g.ID, g, func(r *Run, now time.Time) error {
		if r.Candidate == nil || r.Plan == nil || g.CandidateDigest != r.Candidate.Digest || g.PlanDigest != r.Plan.Digest {
			return ErrConflict
		}
		if !ValidID(g.ID) || g.Executor.ID == "" || (g.Status != "passed" && g.Status != "failed" && g.Status != "error" && g.Status != "skipped") {
			return fmt.Errorf("invalid gate receipt")
		}
		found := false
		optional := false
		for _, c := range r.Plan.Controls {
			if c.ID == g.ControlID {
				found = true
				optional = c.Optional
			}
		}
		if !found {
			return fmt.Errorf("gate is not in plan")
		}
		r.Gates = append(r.Gates, g)
		r.Evidence = append(r.Evidence, g.Evidence...)
		if !optional && (g.Status == "failed" || g.Status == "skipped" || (g.Status == "error" && !g.Retryable)) {
			for _, nc := range r.NonConformances {
				if nc.ControlID == g.ControlID {
					return nil
				}
			}
			r.NonConformances = append(r.NonConformances, NonConformance{ID: r.ID + "-" + g.ControlID, RunID: r.ID, ControlID: g.ControlID, CandidateDigest: r.Candidate.Digest, Policies: r.Plan.Policies, Severity: r.Plan.Risk, Evidence: g.Evidence, DetectedAt: now, Reason: g.Reason, Status: "open"})
		}
		return nil
	})
}

func Attest(r Run, d Decision) (json.RawMessage, error) {
	if d.Result != "approved" || r.Plan == nil || r.Candidate == nil {
		return nil, fmt.Errorf("only an approved candidate can be attested")
	}
	return json.Marshal(map[string]any{"_type": "https://in-toto.io/Statement/v1", "subject": []any{map[string]any{"name": "changes.patch", "digest": map[string]string{"sha256": r.Candidate.PatchDigest[len("sha256:"):]}}}, "predicateType": "https://github.com/mitkox/esf/attestations/patch-readiness/v1", "predicate": map[string]any{"run_id": r.ID, "workflow_id": r.WorkflowID, "execution_id": r.ExecutionID, "candidate": r.Candidate, "plan": r.Plan, "snapshot_digest": r.Snapshot.Digest, "gates": r.Gates, "evidence": r.Evidence, "approval": r.Approval, "exceptions": r.Exceptions, "decision": d, "evaluator": EvaluatorVersion, "signed": false}})
}

// CommitResult verifies content before the transaction and rechecks all domain
// obligations inside it. Blob files are immutable and worker-private.
func (s *Store) CommitResult(ctx context.Context, id string, content *Content, manifest func(Run, Decision, string) (json.RawMessage, error), failure string, cleanupVerified bool) (Run, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Decision != nil {
		return r, nil
	}
	for _, e := range r.Evidence {
		if err = content.Verify(e); err != nil {
			failure = "evidence-integrity: " + err.Error()
			break
		}
	}
	return s.Transition(ctx, id, "finalize", struct {
		Failure string
		Cleanup bool
	}{failure, cleanupVerified}, func(r *Run, now time.Time) error {
		reasons := MissingControls(*r)
		if failure != "" {
			reasons = append(reasons, failure)
		}
		if !cleanupVerified {
			reasons = append(reasons, "cleanup-unverified")
		}
		if r.Approval == nil || r.Approval.Status != "approved" {
			reasons = append(reasons, "approval-not-granted")
		}
		d := Decision{Result: "approved", Reasons: reasons, At: now}
		if len(reasons) > 0 {
			d.Result = "rejected"
		}
		for _, reason := range reasons {
			found := false
			for _, nc := range r.NonConformances {
				if nc.Reason == reason || nc.ControlID == strings.TrimPrefix(reason, "control:") {
					found = true
				}
			}
			if !found {
				policies := []PolicyRef{}
				risk := "unclassified"
				candidate := ""
				if r.Plan != nil {
					policies = r.Plan.Policies
					risk = r.Plan.Risk
				}
				if r.Candidate != nil {
					candidate = r.Candidate.Digest
				}
				r.NonConformances = append(r.NonConformances, NonConformance{ID: r.ID + "-" + Hash(reason)[7:23], RunID: r.ID, ControlID: reason, CandidateDigest: candidate, Policies: policies, Severity: risk, DetectedAt: now, Reason: reason, Status: "open"})
			}
		}
		if d.Result == "approved" {
			att, e := Attest(*r, d)
			if e != nil {
				return e
			}
			r.Attestation = att
			r.AttestationDigest = Digest(att)
		}
		m, e := manifest(*r, d, r.AttestationDigest)
		if e != nil {
			return e
		}
		r.Manifest = m
		r.Decision = &d
		return nil
	})
}
