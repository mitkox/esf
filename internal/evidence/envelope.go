// Package evidence builds and verifies the signed change-evidence bundle.
//
// The bundle is a DSSE envelope around an in-toto Statement. The statement's
// subject digests the patch and, when one exists, the resulting commit. Its
// custom predicate carries the run, input snapshot, gate results and
// environment identity, so a verifier needs no network, CA or transparency log.
package evidence

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const (
	// PayloadType is the in-toto statement media type DSSE wraps.
	PayloadType = "application/vnd.in-toto+json"
	// StatementType is the in-toto Statement version this bundle carries.
	StatementType = "https://in-toto.io/Statement/v1"
	// PredicateType identifies the ESF change-evidence predicate. Its final
	// path segment is the major version; a breaking change moves it.
	PredicateType = "https://esf.dev/evidence/change/v1"
)

// Envelope is a DSSE envelope. Payload and signatures are standard base64.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"`
	Signatures  []Signature `json:"signatures"`
}

// Signature is one detached signature over the pre-authentication encoding.
type Signature struct {
	KeyID string `json:"keyid,omitempty"`
	Sig   string `json:"sig"`
}

// Subject is one artifact the statement attests to.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Statement is the in-toto statement.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Predicate `json:"predicate"`
}

// Predicate is the ESF change-evidence payload. Consumers ignore fields they do
// not know, so a minor version may add fields without changing the type.
type Predicate struct {
	Run          Run          `json:"run"`
	Input        Input        `json:"input"`
	Agent        Agent        `json:"agent"`
	Verification Verification `json:"verification"`
	PolicyDigest string       `json:"policy_digest"`
	NonClaims    []string     `json:"non_claims"`
}

// Run identifies the execution and its durable work item.
type Run struct {
	RunID          string    `json:"run_id"`
	WorkflowID     string    `json:"workflow_id,omitempty"`
	ChangeID       string    `json:"change_id,omitempty"`
	ParentRunID    string    `json:"parent_run_id,omitempty"`
	FactoryVersion string    `json:"factory_version,omitempty"`
	State          string    `json:"state"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
}

// Input is the hashed input snapshot the run executed against.
type Input struct {
	Repository        string `json:"repository"`
	RequestedRevision string `json:"requested_revision"`
	BaseCommit        string `json:"base_commit"`
	ResultCommit      string `json:"result_commit,omitempty"`
	TaskSHA256        string `json:"task_sha256"`
}

// Agent identifies the harness, model and image that produced the change.
type Agent struct {
	Harness         string `json:"harness"`
	Version         string `json:"version,omitempty"`
	Model           string `json:"model,omitempty"`
	SandboxProvider string `json:"sandbox_provider"`
	Image           string `json:"image"`
}

// Verification is the deterministic gate record.
type Verification struct {
	Profile string `json:"profile"`
	Passed  bool   `json:"passed"`
	Gates   []Gate `json:"gates"`
}

// Gate is one deterministic gate. Duration is in nanoseconds, matching the
// factory's recorded verification result.
type Gate struct {
	Name         string        `json:"name"`
	Command      []string      `json:"command"`
	Passed       bool          `json:"passed"`
	TimedOut     bool          `json:"timed_out,omitempty"`
	ExitCode     int           `json:"exit_code"`
	Duration     time.Duration `json:"duration"`
	StdoutDigest string        `json:"stdout_sha256,omitempty"`
	StderrDigest string        `json:"stderr_sha256,omitempty"`
}

// preAuthenticationEncoding is the DSSE v1 PAE:
//
//	"DSSEv1" SP len(payloadType) SP payloadType SP len(payload) SP payload
func preAuthenticationEncoding(payloadType string, payload []byte) []byte {
	var buffer bytes.Buffer
	buffer.WriteString("DSSEv1 ")
	buffer.WriteString(strconv.Itoa(len(payloadType)))
	buffer.WriteByte(' ')
	buffer.WriteString(payloadType)
	buffer.WriteByte(' ')
	buffer.WriteString(strconv.Itoa(len(payload)))
	buffer.WriteByte(' ')
	buffer.Write(payload)
	return buffer.Bytes()
}

// seal signs the payload and wraps it in a DSSE envelope.
func seal(payload []byte, signer Signer) (*Envelope, error) {
	if payload == nil {
		return nil, errors.New("evidence: a statement payload is required")
	}
	if signer == nil {
		return nil, errors.New("evidence: a signer is required")
	}
	signature, err := signer.Sign(preAuthenticationEncoding(PayloadType, payload))
	if err != nil {
		return nil, fmt.Errorf("evidence: sign statement: %w", err)
	}
	return &Envelope{
		PayloadType: PayloadType,
		Payload:     base64.StdEncoding.EncodeToString(payload),
		Signatures:  []Signature{{Sig: base64.StdEncoding.EncodeToString(signature)}},
	}, nil
}
