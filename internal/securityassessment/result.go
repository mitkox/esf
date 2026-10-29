package securityassessment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

func DecodeManifest(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return Manifest{}, errors.New("assessment manifest missing or oversized")
	}
	var result Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Manifest{}, errors.New("malformed assessment manifest")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("trailing assessment manifest data")
	}
	if result.Schema != ManifestSchema {
		return Manifest{}, errors.New("unsupported assessment manifest schema")
	}
	return result, nil
}

const ManifestSchema = "esf.security-assessment/v1"

type CoverageStatus string

const (
	Complete    CoverageStatus = "complete"
	Incomplete  CoverageStatus = "incomplete"
	Unavailable CoverageStatus = "unavailable"
)

type AssertionStatus string

const (
	Passed     AssertionStatus = "passed"
	Failed     AssertionStatus = "failed"
	Unobserved AssertionStatus = "unobserved"
)

type Identity struct {
	RunID               string     `json:"run_id"`
	ChangeID            string     `json:"change_id"`
	ParentRunID         string     `json:"parent_run_id,omitempty"`
	CandidateDigest     string     `json:"candidate_digest"`
	BuiltArtifactDigest string     `json:"built_artifact_digest"`
	TargetInstanceID    string     `json:"target_instance_id"`
	ProfileDigest       string     `json:"profile_digest"`
	RequestDigest       string     `json:"request_digest"`
	RequiredChecks      []string   `json:"required_checks"`
	Limits              Limits     `json:"limits"`
	AI                  AIConfig   `json:"ai"`
	Runtime             RuntimePin `json:"runtime"`
}

func (i Identity) Validate() error {
	for _, id := range []string{i.RunID, i.ChangeID, i.TargetInstanceID} {
		if !identifierPattern.MatchString(id) {
			return errors.New("missing or invalid assessment run/change/target identity")
		}
	}
	if i.ParentRunID != "" && !identifierPattern.MatchString(i.ParentRunID) {
		return errors.New("invalid parent run identity")
	}
	for _, digest := range []string{i.CandidateDigest, i.BuiltArtifactDigest, i.ProfileDigest, i.RequestDigest} {
		if !digestPattern.MatchString(digest) {
			return errors.New("missing or invalid assessment digest")
		}
	}
	if len(i.RequiredChecks) == 0 {
		return errors.New("missing declared assessment checks")
	}
	seen := map[string]bool{}
	for _, id := range i.RequiredChecks {
		if !identifierPattern.MatchString(id) || seen[id] {
			return errors.New("invalid declared assessment checks")
		}
		seen[id] = true
	}
	if i.AI.Enabled && (i.AI.Provider == "" || i.AI.Model == "") {
		return errors.New("optional AI identity requires chosen provider and model")
	}
	if err := i.Limits.Validate(); err != nil {
		return err
	}
	return i.Runtime.Validate()
}

type Coverage struct {
	ID     string         `json:"id"`
	Status CoverageStatus `json:"status"`
	Code   string         `json:"code,omitempty"`
}

type Assertion struct {
	ID            string          `json:"id"`
	Status        AssertionStatus `json:"status"`
	CorrelationID string          `json:"correlation_id,omitempty"`
	EvidenceNames []string        `json:"evidence_names,omitempty"`
}

type AIOutcome struct {
	Status   string `json:"status"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type Cleanup struct {
	Status            CoverageStatus `json:"status"`
	OwnedIDs          []string       `json:"owned_ids,omitempty"`
	RemainingIDs      []string       `json:"remaining_ids,omitempty"`
	PendingAllocation bool           `json:"pending_allocation"`
}

type EvidenceRef struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Manifest struct {
	Schema         string        `json:"schema"`
	Identity       Identity      `json:"identity"`
	Coverage       []Coverage    `json:"coverage"`
	Assertions     []Assertion   `json:"assertions"`
	Evidence       []EvidenceRef `json:"evidence"`
	AI             AIOutcome     `json:"ai"`
	Cleanup        Cleanup       `json:"cleanup"`
	Truncated      bool          `json:"truncated"`
	verifiedDigest string
}

func safeEvidenceName(name string) bool {
	return name != "" && len(name) <= 256 && !path.IsAbs(name) && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00\r\n")
}

func validCoverage(status CoverageStatus) bool {
	return status == Complete || status == Incomplete || status == Unavailable
}

func (m Manifest) Validate(expected Identity) error {
	if m.Schema != ManifestSchema {
		return errors.New("unsupported assessment manifest schema")
	}
	if err := m.Identity.Validate(); err != nil {
		return err
	}
	if err := expected.Validate(); err != nil {
		return err
	}
	actualJSON, _ := json.Marshal(m.Identity)
	expectedJSON, _ := json.Marshal(expected)
	if string(actualJSON) != string(expectedJSON) {
		return errors.New("assessment identity mismatch")
	}
	if len(m.Coverage) == 0 || len(m.Assertions) == 0 || len(m.Evidence) == 0 || m.Truncated {
		return errors.New("assessment coverage, assertions or evidence missing/truncated")
	}
	coverage := map[string]bool{}
	for _, c := range m.Coverage {
		if !identifierPattern.MatchString(c.ID) || coverage[c.ID] || !validCoverage(c.Status) || (c.Code != "" && !identifierPattern.MatchString(c.Code)) {
			return errors.New("invalid assessment coverage")
		}
		coverage[c.ID] = true
	}
	assertions := map[string]bool{}
	for _, a := range m.Assertions {
		if !identifierPattern.MatchString(a.ID) || assertions[a.ID] || (a.Status != Passed && a.Status != Failed && a.Status != Unobserved) || (a.CorrelationID != "" && !identifierPattern.MatchString(a.CorrelationID)) {
			return errors.New("invalid assessment assertion")
		}
		assertions[a.ID] = true
	}
	for _, id := range expected.RequiredChecks {
		if !coverage[id] || !assertions[id] {
			return errors.New("declared assessment check omitted")
		}
	}
	if !validCoverage(m.Cleanup.Status) {
		return errors.New("missing or invalid cleanup result")
	}
	if m.AI.Status != "not-requested" && m.AI.Status != "complete" && m.AI.Status != "incomplete" && m.AI.Status != "unavailable" {
		return errors.New("missing or invalid optional AI outcome")
	}
	if m.AI.Status != "not-requested" && (m.AI.Provider == "" || m.AI.Model == "") {
		return errors.New("AI outcome lost configured provider or model")
	}
	if expected.AI.Enabled {
		if m.AI.Status == "not-requested" || m.AI.Provider != expected.AI.Provider || m.AI.Model != expected.AI.Model {
			return errors.New("optional AI outcome changed configured provider or model")
		}
	} else if m.AI.Status != "not-requested" {
		return errors.New("AI result was not requested by profile")
	}
	seen := map[string]bool{}
	for _, e := range m.Evidence {
		if !safeEvidenceName(e.Name) || seen[e.Name] || !digestPattern.MatchString(e.SHA256) || e.Bytes <= 0 || e.Bytes > 16<<20 {
			return errors.New("invalid evidence reference")
		}
		seen[e.Name] = true
	}
	for _, a := range m.Assertions {
		for _, name := range a.EvidenceNames {
			if !seen[name] {
				return errors.New("assertion references missing evidence")
			}
		}
	}
	return nil
}

func VerifyEvidence(data []byte, expectedSHA256 string) error {
	if !digestPattern.MatchString(expectedSHA256) || SHA256(data) != expectedSHA256 {
		return errors.New("evidence digest mismatch")
	}
	return nil
}

// Verify requires the controller's independently held hashes, never hashes
// recovered from target-writable state as their own expected value.
func (m *Manifest) Verify(expected Identity, exports map[string][]byte, expectedHashes map[string]string) error {
	m.verifiedDigest = ""
	if err := m.Validate(expected); err != nil {
		return err
	}
	if len(exports) != len(m.Evidence) || len(expectedHashes) != len(m.Evidence) {
		return errors.New("selected evidence set mismatch")
	}
	var total int64
	for _, e := range m.Evidence {
		data, ok := exports[e.Name]
		if !ok || int64(len(data)) != e.Bytes || expectedHashes[e.Name] != e.SHA256 {
			return errors.New("missing or inconsistent selected evidence")
		}
		total += int64(len(data))
		if total > expected.Limits.MaxEvidenceBytes {
			return errors.New("selected evidence exceeds profile bound")
		}
		if err := VerifyEvidence(data, expectedHashes[e.Name]); err != nil {
			return fmt.Errorf("selected evidence %s: %w", e.Name, err)
		}
	}
	data, _ := json.Marshal(m)
	m.verifiedDigest = SHA256(data)
	return nil
}

func (m Manifest) Passed() bool {
	if m.verifiedDigest == "" || m.Validate(m.Identity) != nil {
		return false
	}
	data, _ := json.Marshal(m)
	if SHA256(data) != m.verifiedDigest {
		return false
	}
	if m.Cleanup.Status != Complete || m.Cleanup.PendingAllocation || len(m.Cleanup.RemainingIDs) > 0 {
		return false
	}
	for _, c := range m.Coverage {
		if c.Status != Complete {
			return false
		}
	}
	for _, a := range m.Assertions {
		if a.Status != Passed {
			return false
		}
	}
	return true
}
