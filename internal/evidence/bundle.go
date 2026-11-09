package evidence

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// Record is the completed run and change data a bundle attests to. It is
// assembled from the run manifest and the change record; the builder adds no
// fact of its own.
type Record struct {
	RunID          string
	WorkflowID     string
	ChangeID       string
	ParentRunID    string
	FactoryVersion string
	State          string
	StartedAt      time.Time
	CompletedAt    time.Time

	Repository        string
	RequestedRevision string
	BaseCommit        string
	ResultCommit      string
	TaskHash          string
	Patch             []byte

	Agent        Agent
	Verification Verification
	PolicyDigest string
}

// Signer signs a DSSE pre-authentication encoding. The private key never
// enters a sandbox; the factory host supplies the signer.
type Signer interface {
	Sign(message []byte) ([]byte, error)
}

// Ed25519Signer signs with a factory-held ed25519 private key.
type Ed25519Signer struct {
	Key ed25519.PrivateKey
}

// Sign implements Signer.
func (s Ed25519Signer) Sign(message []byte) ([]byte, error) {
	if len(s.Key) != ed25519.PrivateKeySize {
		return nil, errors.New("evidence: an ed25519 private key is required")
	}
	return ed25519.Sign(s.Key, message), nil
}

// Build produces a signed bundle for one completed run.
func Build(record Record, signer Signer) (*Envelope, error) {
	payload, err := statementPayload(record)
	if err != nil {
		return nil, err
	}
	return seal(payload, signer)
}

// statementPayload encodes the statement the signature covers.
func statementPayload(record Record) ([]byte, error) {
	payload, err := json.Marshal(Statement{
		Type:          StatementType,
		Subject:       bundleSubjects(record),
		PredicateType: PredicateType,
		Predicate:     predicateFor(record),
	})
	if err != nil {
		return nil, fmt.Errorf("evidence: encode statement: %w", err)
	}
	return payload, nil
}

// bundleSubjects digests the patch and, when the run produced one, the
// resulting commit. A git commit id is named with the hash algorithm its length
// implies: forty hex characters are SHA-1, sixty-four are SHA-256.
func bundleSubjects(record Record) []Subject {
	patch := sha256.Sum256(record.Patch)
	subjects := []Subject{{
		Name:   "changes.patch",
		Digest: map[string]string{"sha256": hex.EncodeToString(patch[:])},
	}}
	if record.ResultCommit != "" {
		algorithm := "sha1"
		if len(record.ResultCommit) == sha256.Size*2 {
			algorithm = "sha256"
		}
		subjects = append(subjects, Subject{
			Name:   "git-commit",
			Digest: map[string]string{algorithm: record.ResultCommit},
		})
	}
	return subjects
}

func predicateFor(record Record) Predicate {
	return Predicate{
		Run: Run{
			RunID:          record.RunID,
			WorkflowID:     record.WorkflowID,
			ChangeID:       record.ChangeID,
			ParentRunID:    record.ParentRunID,
			FactoryVersion: record.FactoryVersion,
			State:          record.State,
			StartedAt:      record.StartedAt,
			CompletedAt:    record.CompletedAt,
		},
		Input: Input{
			Repository:        record.Repository,
			RequestedRevision: record.RequestedRevision,
			BaseCommit:        record.BaseCommit,
			ResultCommit:      record.ResultCommit,
			TaskSHA256:        record.TaskHash,
		},
		Agent:        record.Agent,
		Verification: record.Verification,
		PolicyDigest: record.PolicyDigest,
		NonClaims:    nonClaims(),
	}
}

// nonClaims states what the bundle does not establish. They are part of the
// evidence, not a disclaimer added at display time.
func nonClaims() []string {
	return []string{
		"Attests only to the run, input snapshot and gate results recorded in this bundle.",
		"Does not claim that the change is correct beyond the named gates, or that an agent's reasoning or output is trustworthy.",
		"Does not claim forge acceptance, merge safety, deployment, production readiness, or behavior outside the recorded run.",
	}
}

// ParsePrivateKey reads a PKCS#8 PEM ed25519 private key.
func ParsePrivateKey(body []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("evidence: private key must be PEM encoded")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("evidence: parse private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("evidence: private key is not ed25519")
	}
	return key, nil
}

// ParsePublicKey reads a PKIX PEM ed25519 public key.
func ParsePublicKey(body []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("evidence: public key must be PEM encoded")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("evidence: parse public key: %w", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("evidence: public key is not ed25519")
	}
	return key, nil
}
