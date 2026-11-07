// Package api serves the versioned public execution API.
//
// It is a thin HTTP layer over an execution controller: it owns the wire
// contract, authentication, TLS and request bounds, and nothing about how a run
// executes. It never accepts an executable, a host path or a credential.
package api

import (
	"encoding/json"
	"time"
)

// RunRequest is the submission body. It names the three caller inputs (an
// approved repository, an exact revision and task text) plus operator-declared
// resource names. It cannot supply a host path, an executable, a template id or
// a credential.
type RunRequest struct {
	RunID      string `json:"run_id"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Task       string `json:"task"`

	// Named operator resources. An empty name falls back to the operator's
	// configured default; it never means "no policy".
	AgentHarness        string `json:"agent_harness"`
	AgentModel          string `json:"agent_model,omitempty"`
	VerificationProfile string `json:"verification_profile"`
	Scope               string `json:"scope,omitempty"`
	Workspace           string `json:"workspace,omitempty"`
	Model               string `json:"model,omitempty"`
	EgressPolicy        string `json:"egress_policy,omitempty"`

	// Durable work-item lineage.
	ChangeID    string `json:"change_id,omitempty"`
	ParentRunID string `json:"parent_run_id,omitempty"`
}

// Condition is one step outcome observed for a run.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"last_transition_time"`
}

// RunStatus is a run's current state and evidence summary.
type RunStatus struct {
	RunID               string      `json:"run_id"`
	WorkflowID          string      `json:"workflow_id"`
	State               string      `json:"state"`
	CurrentStep         string      `json:"current_step,omitempty"`
	Repository          string      `json:"repository,omitempty"`
	Revision            string      `json:"revision,omitempty"`
	Scope               string      `json:"scope,omitempty"`
	ChangeID            string      `json:"change_id,omitempty"`
	AgentHarness        string      `json:"agent_harness,omitempty"`
	VerificationProfile string      `json:"verification_profile,omitempty"`
	AgentResult         string      `json:"agent_result,omitempty"`
	VerificationResult  string      `json:"verification_result,omitempty"`
	StartedAt           time.Time   `json:"started_at"`
	CompletedAt         time.Time   `json:"completed_at"`
	Conditions          []Condition `json:"conditions,omitempty"`
	Error               string      `json:"error,omitempty"`
}

// Event is one sequenced observation from a run. Events are derived from the
// run's conditions, so a cursor is stable across polls.
type Event struct {
	Seq     int       `json:"seq"`
	Type    string    `json:"type"`
	Status  string    `json:"status,omitempty"`
	Reason  string    `json:"reason,omitempty"`
	Message string    `json:"message,omitempty"`
	Time    time.Time `json:"time"`
}

// Evidence is a run's durable manifest, returned byte for byte.
type Evidence struct {
	RunID    string          `json:"run_id"`
	Manifest json.RawMessage `json:"manifest"`
}

// errorResponse is the machine-readable error body.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}
