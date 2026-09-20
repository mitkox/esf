package factory

import (
	"fmt"
	"time"
)

// Conditions are the factory's AX-style lifecycle signal.
//
// A run state answers "what is happening now" and is intentionally coarse: it
// must be reconstructible from the final manifest alone. Conditions answer the
// operational question the MVP could not: which of the run's independent steps
// actually completed, and when. They are the reason an operator can see that a
// run failed in verification rather than in repository preparation without
// reading the Temporal history.
//
// Conditions are recorded by the WORKFLOW, not by activities: they are derived
// from activity results and therefore deterministic under replay. The timestamp
// is workflow.Now, never wall-clock time.

// ConditionType names one step whose outcome is tracked independently.
type ConditionType string

const (
	// ConditionValidated reports that the request passed operator policy.
	ConditionValidated ConditionType = "Validated"
	// ConditionSandboxReady reports that the microVM exists.
	ConditionSandboxReady ConditionType = "SandboxReady"
	// ConditionWorkspacePrepared reports that factory-owned provisioning finished.
	ConditionWorkspacePrepared ConditionType = "WorkspacePrepared"
	// ConditionRepositoryPrepared reports that the requested revision is checked out.
	ConditionRepositoryPrepared ConditionType = "RepositoryPrepared"
	// ConditionInventoryWritten reports that the sandbox inventory is available
	// to the agent. A harness that cannot see its inventory is running blind,
	// so this is tracked separately from repository preparation.
	ConditionInventoryWritten ConditionType = "InventoryWritten"
	// ConditionAgentCompleted reports that the harness process exited.
	ConditionAgentCompleted ConditionType = "AgentCompleted"
	// ConditionVerified reports that every mandatory deterministic gate passed.
	ConditionVerified ConditionType = "Verified"
	// ConditionArtifactsCollected reports that the deliverable patch was captured.
	ConditionArtifactsCollected ConditionType = "ArtifactsCollected"
	// ConditionReviewPaused reports that the run is waiting on a human.
	ConditionReviewPaused ConditionType = "ReviewPaused"
	// ConditionCleanupVerified reports that no sandbox belonging to the run remains.
	ConditionCleanupVerified ConditionType = "CleanupVerified"
	// ConditionEvidenceWritten reports that the durable manifest was persisted.
	ConditionEvidenceWritten ConditionType = "EvidenceWritten"
	// ConditionBudgetExceeded reports that recorded usage crossed a configured
	// ceiling. It is evidence, never enforcement: the factory does not rewrite a
	// result because a budget was hit.
	ConditionBudgetExceeded ConditionType = "BudgetExceeded"
)

// ConditionStatus is the tri-state used by every condition.
type ConditionStatus string

const (
	// ConditionTrue means the step completed successfully.
	ConditionTrue ConditionStatus = "True"
	// ConditionFalse means the step ran and failed.
	ConditionFalse ConditionStatus = "False"
	// ConditionUnknown means the step has not run yet, or its outcome is not
	// yet meaningful. It is the initial state of every condition that exists
	// during a run.
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition is one step's observed outcome.
type Condition struct {
	// Type identifies the step.
	Type ConditionType `json:"type"`
	// Status is True, False or Unknown.
	Status ConditionStatus `json:"status"`
	// Reason is a short machine-readable cause, for example "ExitZero" or
	// "GateFailed".
	Reason string `json:"reason,omitempty"`
	// Message is a bounded human-readable detail. It is redacted before it is
	// written, like every other piece of evidence.
	Message string `json:"message,omitempty"`
	// LastTransitionTime is the workflow time of the last change. It comes from
	// workflow.Now so replay reconstructs it identically.
	LastTransitionTime time.Time `json:"last_transition_time"`
}

// SetCondition records a step outcome, replacing any previous entry of the same
// type.
//
// The function is intentionally tolerant of a nil slice so callers cannot
// forget to initialise. It never removes a condition: "cleanup was not
// verified" is exactly the fact an auditor needs, so a failed condition stays
// in the list.
func SetCondition(conditions []Condition, c Condition) []Condition {
	out := make([]Condition, 0, len(conditions)+1)
	replaced := false
	for _, existing := range conditions {
		if existing.Type == c.Type {
			out = append(out, c)
			replaced = true
			continue
		}
		out = append(out, existing)
	}
	if !replaced {
		out = append(out, c)
	}
	return out
}

// ConditionFor returns the current entry for a type, if present.
func ConditionFor(conditions []Condition, t ConditionType) (Condition, bool) {
	for _, c := range conditions {
		if c.Type == t {
			return c, true
		}
	}
	return Condition{}, false
}

// ConditionSummary renders conditions for a CLI or log line, for example
// "SandboxReady=True AgentCompleted=False".
func ConditionSummary(conditions []Condition) string {
	if len(conditions) == 0 {
		return ""
	}
	out := ""
	for i, c := range conditions {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s=%s", c.Type, c.Status)
	}
	return out
}

// ConditionCounts counts conditions by status. The CLI uses it for the compact
// one-line summary in `factory get runs`.
func ConditionCounts(conditions []Condition) (trueCount, falseCount, unknownCount int) {
	for _, c := range conditions {
		switch c.Status {
		case ConditionTrue:
			trueCount++
		case ConditionFalse:
			falseCount++
		default:
			unknownCount++
		}
	}
	return trueCount, falseCount, unknownCount
}
