package evidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return publicKey, privateKey
}

func sampleRecord() Record {
	return Record{
		RunID:             "run-fixture",
		WorkflowID:        "factory-run-run-fixture",
		ChangeID:          "change-fixture",
		FactoryVersion:    "1.0.0",
		State:             "SUCCEEDED",
		StartedAt:         time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC),
		CompletedAt:       time.Date(2026, 11, 7, 10, 5, 0, 0, time.UTC),
		Repository:        "https://github.com/acme/repository",
		RequestedRevision: strings.Repeat("a", 40),
		BaseCommit:        strings.Repeat("a", 40),
		ResultCommit:      strings.Repeat("b", 40),
		TaskHash:          strings.Repeat("c", 64),
		Patch:             []byte("diff --git a/main.go b/main.go\n+func main() {}\n"),
		Agent: Agent{
			Harness:         "opencode",
			Version:         "1.4.0",
			Model:           "gpt-5",
			SandboxProvider: "cube",
			Image:           "tpl-fixture",
		},
		Verification: Verification{
			Profile: "default",
			Passed:  true,
			Gates: []Gate{
				{
					Name:         "build",
					Command:      []string{"./build.sh"},
					Passed:       true,
					ExitCode:     0,
					Duration:     2 * time.Second,
					StdoutDigest: strings.Repeat("d", 64),
					StderrDigest: strings.Repeat("e", 64),
				},
			},
		},
		PolicyDigest: strings.Repeat("f", 64),
	}
}

func TestBuildCarriesRequiredEvidence(t *testing.T) {
	record := sampleRecord()
	publicKey, privateKey := testKey(t)
	envelope, err := Build(record, Ed25519Signer{Key: privateKey})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	statement, err := Verify(envelope, publicKey)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if statement.Type != StatementType {
		t.Fatalf("statement type = %q", statement.Type)
	}
	if statement.PredicateType != PredicateType || !strings.HasSuffix(statement.PredicateType, "/v1") {
		t.Fatalf("predicate type = %q, want a versioned type", statement.PredicateType)
	}
	if len(statement.Subject) != 2 {
		t.Fatalf("subjects = %#v, want patch and commit", statement.Subject)
	}
	patchDigest := sha256.Sum256(record.Patch)
	if statement.Subject[0].Name != "changes.patch" || statement.Subject[0].Digest["sha256"] != hex.EncodeToString(patchDigest[:]) {
		t.Fatalf("patch subject = %#v", statement.Subject[0])
	}
	if statement.Subject[1].Name != "git-commit" || statement.Subject[1].Digest["sha1"] != record.ResultCommit {
		t.Fatalf("commit subject = %#v", statement.Subject[1])
	}

	predicate := statement.Predicate
	if predicate.Run.RunID != record.RunID || predicate.Run.WorkflowID != record.WorkflowID || predicate.Run.ChangeID != record.ChangeID {
		t.Fatalf("run = %#v", predicate.Run)
	}
	if predicate.Run.FactoryVersion != record.FactoryVersion || predicate.Run.State != record.State {
		t.Fatalf("run identity = %#v", predicate.Run)
	}
	if !predicate.Run.StartedAt.Equal(record.StartedAt) || !predicate.Run.CompletedAt.Equal(record.CompletedAt) {
		t.Fatalf("run timestamps = %#v", predicate.Run)
	}
	if predicate.Input.Repository != record.Repository || predicate.Input.RequestedRevision != record.RequestedRevision {
		t.Fatalf("input = %#v", predicate.Input)
	}
	if predicate.Input.BaseCommit != record.BaseCommit || predicate.Input.ResultCommit != record.ResultCommit || predicate.Input.TaskSHA256 != record.TaskHash {
		t.Fatalf("input commits = %#v", predicate.Input)
	}
	if predicate.Agent.Harness != record.Agent.Harness || predicate.Agent.Version != record.Agent.Version || predicate.Agent.Model != record.Agent.Model {
		t.Fatalf("agent = %#v", predicate.Agent)
	}
	if predicate.Agent.SandboxProvider != record.Agent.SandboxProvider || predicate.Agent.Image != record.Agent.Image {
		t.Fatalf("agent environment = %#v", predicate.Agent)
	}
	if predicate.PolicyDigest != record.PolicyDigest {
		t.Fatalf("policy digest = %q", predicate.PolicyDigest)
	}
	if predicate.Verification.Profile != record.Verification.Profile || !predicate.Verification.Passed {
		t.Fatalf("verification = %#v", predicate.Verification)
	}
	if len(predicate.Verification.Gates) != 1 {
		t.Fatalf("gates = %#v", predicate.Verification.Gates)
	}
	gate := predicate.Verification.Gates[0]
	if gate.Name != "build" || len(gate.Command) != 1 || !gate.Passed || gate.Duration != 2*time.Second {
		t.Fatalf("gate = %#v", gate)
	}
	if gate.StdoutDigest != strings.Repeat("d", 64) || gate.StderrDigest != strings.Repeat("e", 64) {
		t.Fatalf("gate log digests = %#v", gate)
	}
	if len(predicate.NonClaims) == 0 {
		t.Fatal("bundle carries no non-claims")
	}
}

func TestVerifySucceedsOfflineWithMatchingKey(t *testing.T) {
	record := sampleRecord()
	publicKey, privateKey := testKey(t)
	envelope, err := Build(record, Ed25519Signer{Key: privateKey})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	statement, err := Verify(envelope, publicKey)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if statement.Predicate.Run.RunID != record.RunID {
		t.Fatalf("run = %q", statement.Predicate.Run.RunID)
	}
}

func TestVerifyRejectsAlteredGateResult(t *testing.T) {
	publicKey, privateKey := testKey(t)
	envelope, err := Build(sampleRecord(), Ed25519Signer{Key: privateKey})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("decode statement: %v", err)
	}
	predicate := document["predicate"].(map[string]any)
	verification := predicate["verification"].(map[string]any)
	gates := verification["gates"].([]any)
	gates[0].(map[string]any)["passed"] = false
	tampered, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode tampered statement: %v", err)
	}
	envelope.Payload = base64.StdEncoding.EncodeToString(tampered)

	if _, err := Verify(envelope, publicKey); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered bundle must fail with a signature mismatch, got %v", err)
	}
}

func TestVerifyFailsClosedForNonMatchingKey(t *testing.T) {
	publicKey, _ := testKey(t)
	_, otherPrivateKey := testKey(t)
	envelope, err := Build(sampleRecord(), Ed25519Signer{Key: otherPrivateKey})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := Verify(envelope, publicKey); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a bundle signed by another key must fail closed, got %v", err)
	}
}

func TestVerifyIgnoresUnknownPredicateFields(t *testing.T) {
	publicKey, privateKey := testKey(t)
	payload, err := statementPayload(sampleRecord())
	if err != nil {
		t.Fatalf("statementPayload: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("decode statement: %v", err)
	}
	predicate := document["predicate"].(map[string]any)
	predicate["future_field"] = map[string]any{"added_by": "a later minor version"}
	extended, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode extended statement: %v", err)
	}
	envelope, err := seal(extended, Ed25519Signer{Key: privateKey})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	statement, err := Verify(envelope, publicKey)
	if err != nil {
		t.Fatalf("unknown predicate fields must be ignored: %v", err)
	}
	if statement.Predicate.Run.RunID != "run-fixture" {
		t.Fatalf("run = %q", statement.Predicate.Run.RunID)
	}
}

func TestParseEd25519Keys(t *testing.T) {
	publicKey, privateKey := testKey(t)
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	parsedPrivate, err := ParsePrivateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	if !privateKey.Equal(parsedPrivate) {
		t.Fatal("parsed private key differs")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	parsedPublic, err := ParsePublicKey(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	if !publicKey.Equal(parsedPublic) {
		t.Fatal("parsed public key differs")
	}
}

func TestFixtureBundleVerifiesWithCheckedInPublicKey(t *testing.T) {
	bundleBody, err := os.ReadFile(filepath.Join("testdata", "fixture-bundle.json"))
	if err != nil {
		t.Fatalf("read fixture bundle: %v", err)
	}
	keyBody, err := os.ReadFile(filepath.Join("testdata", "fixture-public.txt"))
	if err != nil {
		t.Fatalf("read fixture public key: %v", err)
	}
	var envelope Envelope
	if err := json.Unmarshal(bundleBody, &envelope); err != nil {
		t.Fatalf("decode fixture bundle: %v", err)
	}
	publicKey, err := ParsePublicKey(keyBody)
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}
	statement, err := Verify(&envelope, publicKey)
	if err != nil {
		t.Fatalf("fixture bundle did not verify: %v", err)
	}
	if statement.Predicate.Run.RunID != "run-fixture" {
		t.Fatalf("fixture run = %q", statement.Predicate.Run.RunID)
	}
}
