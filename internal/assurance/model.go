// Package assurance implements the policy and evidence contracts of the factory.
// It does not orchestrate execution: Temporal remains the workflow engine.
package assurance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const EvaluatorVersion = "esf-assurance/v1"

var (
	ErrNotFound  = errors.New("quality record not found")
	ErrConflict  = errors.New("quality integrity conflict")
	ErrForbidden = errors.New("quality action forbidden")
	ErrTerminal  = errors.New("quality record is terminal")
)

func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func Hash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("assurance: unhashable value: %v", err))
	}
	return Digest(b)
}

type Actor struct {
	ID    string   `json:"id"`
	UID   uint32   `json:"uid"`
	Roles []string `json:"roles,omitempty"`
	Kind  string   `json:"kind"` // human, agent, or factory; assigned by the service
}

type PolicyRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type Snapshot struct {
	Evaluator    string          `json:"evaluator"`
	RepositoryID string          `json:"repository_id"`
	Policies     []Policy        `json:"policies"`
	Dependencies json.RawMessage `json:"dependencies"`
	Digest       string          `json:"digest"`
}

type Candidate struct {
	RepositoryID   string   `json:"repository_id"`
	BaselineSHA    string   `json:"baseline_sha"`
	BaselineTree   string   `json:"baseline_tree"`
	BaselineBundle string   `json:"baseline_bundle"`
	PatchDigest    string   `json:"patch_digest"`
	TreeDigest     string   `json:"tree_digest"`
	Paths          []string `json:"paths"`
	Digest         string   `json:"digest"`
}

type Plan struct {
	Policies       []PolicyRef     `json:"policies"`
	Risk           string          `json:"risk"`
	Rank           int             `json:"rank"`
	Controls       []Control       `json:"controls"`
	Evidence       []string        `json:"evidence"`
	Approvals      []ApprovalRule  `json:"approvals"`
	Qualifications []Qualification `json:"qualifications"`
	Exceptions     []ExceptionRule `json:"exceptions"`
	Digest         string          `json:"digest"`
}

type Evidence struct {
	Kind            string `json:"kind"`
	Digest          string `json:"digest"`
	Size            int64  `json:"size"`
	Producer        string `json:"producer"`
	Origin          string `json:"origin"` // factory-observed or repository-reported
	CandidateDigest string `json:"candidate_digest,omitempty"`
	ControlID       string `json:"control_id,omitempty"`
	Redacted        bool   `json:"redacted,omitempty"`
}

type GateAttempt struct {
	ID              string     `json:"id"`
	ControlID       string     `json:"control_id"`
	CandidateDigest string     `json:"candidate_digest"`
	PlanDigest      string     `json:"plan_digest"`
	Status          string     `json:"status"` // passed, failed, error, skipped
	Reason          string     `json:"reason,omitempty"`
	Retryable       bool       `json:"retryable"`
	NonWaivable     bool       `json:"non_waivable,omitempty"`
	Executor        Actor      `json:"executor"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     time.Time  `json:"completed_at"`
	Evidence        []Evidence `json:"evidence"`
}

type ReviewReport struct {
	CandidateDigest string   `json:"candidate_digest"`
	PlanDigest      string   `json:"plan_digest"`
	Verdict         string   `json:"verdict"`
	Findings        []string `json:"findings"`
}

type ApprovalDecision struct {
	Actor    Actor     `json:"actor"`
	Decision string    `json:"decision"`
	Reason   string    `json:"reason"`
	At       time.Time `json:"at"`
}
type ApprovalRequest struct {
	ID                  string             `json:"id"`
	CandidateDigest     string             `json:"candidate_digest"`
	PlanDigest          string             `json:"plan_digest"`
	Rules               []ApprovalRule     `json:"rules"`
	Deadline            time.Time          `json:"deadline"`
	RequestedAt         time.Time          `json:"requested_at"`
	DispositionDeadline *time.Time         `json:"disposition_deadline,omitempty"`
	Status              string             `json:"status"`
	Decisions           []ApprovalDecision `json:"decisions"`
}
type Exception struct {
	ControlID       string    `json:"control_id"`
	CandidateDigest string    `json:"candidate_digest"`
	PlanDigest      string    `json:"plan_digest"`
	Actor           Actor     `json:"actor"`
	Reason          string    `json:"reason"`
	At              time.Time `json:"at"`
}
type Decision struct {
	Result  string    `json:"result"` // approved, rejected, blocked
	Reasons []string  `json:"reasons"`
	At      time.Time `json:"at"`
}
type NonConformance struct {
	ID              string      `json:"id"`
	RunID           string      `json:"run_id"`
	ControlID       string      `json:"control_id"`
	CandidateDigest string      `json:"candidate_digest"`
	Policies        []PolicyRef `json:"policies"`
	Severity        string      `json:"severity"`
	Evidence        []Evidence  `json:"evidence"`
	DetectedAt      time.Time   `json:"detected_at"`
	Reason          string      `json:"reason"`
	Status          string      `json:"status"`
	Disposition     string      `json:"disposition,omitempty"`
}

type Run struct {
	ID                string                     `json:"id"`
	AdmissionID       string                     `json:"admission_id"`
	RequestDigest     string                     `json:"request_digest"`
	SubmissionDigest  string                     `json:"submission_digest,omitempty"`
	Request           json.RawMessage            `json:"request"`
	Requester         Actor                      `json:"requester"`
	Snapshot          Snapshot                   `json:"snapshot"`
	WorkflowID        string                     `json:"workflow_id"`
	ExecutionID       string                     `json:"execution_id,omitempty"`
	CreatedAt         time.Time                  `json:"created_at"`
	Candidate         *Candidate                 `json:"candidate,omitempty"`
	Plan              *Plan                      `json:"plan,omitempty"`
	Gates             []GateAttempt              `json:"gates"`
	Evidence          []Evidence                 `json:"evidence"`
	Approval          *ApprovalRequest           `json:"approval,omitempty"`
	Exceptions        []Exception                `json:"exceptions"`
	NonConformances   []NonConformance           `json:"non_conformances"`
	Checkpoints       map[string]json.RawMessage `json:"checkpoints"`
	Decision          *Decision                  `json:"decision,omitempty"`
	Manifest          json.RawMessage            `json:"manifest,omitempty"`
	Attestation       json.RawMessage            `json:"attestation,omitempty"`
	AttestationDigest string                     `json:"attestation_digest,omitempty"`
}

type Event struct {
	Sequence  int64           `json:"sequence"`
	Aggregate string          `json:"aggregate"`
	Operation string          `json:"operation"`
	Type      string          `json:"type"`
	At        time.Time       `json:"at"`
	Data      json.RawMessage `json:"data"`
}
type Outbox struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Aggregate string          `json:"aggregate"`
	Payload   json.RawMessage `json:"payload"`
	Attempts  int             `json:"attempts"`
	Error     string          `json:"error,omitempty"`
}

type RiskClassifier interface {
	Classify(context.Context, Snapshot, Candidate) (Plan, error)
}
type GateExecutor interface {
	Execute(context.Context, Run, Control, string) (GateAttempt, error)
}
type QualityStore interface {
	Reserve(context.Context, Run) (Run, error)
	Get(context.Context, string) (Run, error)
	List(context.Context) ([]Run, error)
	Transition(context.Context, string, string, any, func(*Run, time.Time) error) (Run, error)
	Events(context.Context, string) ([]Event, error)
	Pending(context.Context) ([]Outbox, error)
	Close() error
}

// ReleaseRequest is a future shipping boundary. A patch decision is never a
// release authorization. There is deliberately no permissive implementation.
type ReleaseRequest struct {
	ArtifactDigest         string     `json:"artifact_digest"`
	RepositoryID           string     `json:"repository_id"`
	SourceRevision         string     `json:"source_revision"`
	PatchAttestationDigest string     `json:"patch_attestation_digest"`
	Target                 string     `json:"target"`
	Evidence               []Evidence `json:"evidence"`
	Authority              Actor      `json:"authority"`
}
type ReleaseAuthorizer interface {
	AuthorizeRelease(context.Context, ReleaseRequest) (Decision, error)
}
