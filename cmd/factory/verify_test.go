package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	verifyFixtureBundle = "../../internal/evidence/testdata/fixture-bundle.json"
	verifyFixtureKey    = "../../internal/evidence/testdata/fixture-public.txt"
)

// writeReadableKey copies the checked-in public key to a temp file with the
// owner-only permissions the private-file reader requires.
func writeReadableKey(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(verifyFixtureKey)
	if err != nil {
		t.Fatalf("read fixture key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "public.txt")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path
}

func runVerify(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := newVerifyCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	command.SetArgs(args)
	err := command.Execute()
	return out.String(), err
}

func TestVerifyCommandPrintsVerifiedFixture(t *testing.T) {
	out, err := runVerify(t, "--bundle", verifyFixtureBundle, "--key", writeReadableKey(t))
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Result:    VERIFIED") {
		t.Fatalf("output does not report verification:\n%s", out)
	}
	if !strings.Contains(out, "run-fixture") {
		t.Fatalf("output does not name the run:\n%s", out)
	}
}

func TestVerifyCommandRejectsTamperedSignature(t *testing.T) {
	body, err := os.ReadFile(verifyFixtureBundle)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	signatures, ok := document["signatures"].([]any)
	if !ok || len(signatures) == 0 {
		t.Fatalf("fixture has no signatures")
	}
	first, ok := signatures[0].(map[string]any)
	if !ok {
		t.Fatalf("fixture signature is not an object")
	}
	encoded, ok := first["sig"].(string)
	if !ok {
		t.Fatalf("fixture signature has no sig field")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	raw[0] ^= 0xff
	first["sig"] = base64.StdEncoding.EncodeToString(raw)
	tampered, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode tampered bundle: %v", err)
	}
	path := filepath.Join(t.TempDir(), "tampered.json")
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatalf("write tampered bundle: %v", err)
	}
	out, err := runVerify(t, "--bundle", path, "--key", writeReadableKey(t))
	if err == nil {
		t.Fatalf("a tampered bundle verified:\n%s", out)
	}
}

func TestVerifyCommandRequiresBothFlags(t *testing.T) {
	if _, err := runVerify(t, "--key", writeReadableKey(t)); err == nil {
		t.Fatal("missing --bundle was accepted")
	}
	if _, err := runVerify(t, "--bundle", verifyFixtureBundle); err == nil {
		t.Fatal("missing --key was accepted")
	}
}

func TestVerifyCommandRejectsNonRegularBundle(t *testing.T) {
	_, err := runVerify(t, "--bundle", filepath.Dir(verifyFixtureBundle), "--key", writeReadableKey(t))
	if err == nil {
		t.Fatal("a directory was accepted as a bundle")
	}
}

func TestVerifyCommandRejectsOversizedBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	if err := file.Truncate(maxBundleBytes + 1); err != nil {
		t.Fatalf("truncate bundle: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close bundle: %v", err)
	}
	if _, err := runVerify(t, "--bundle", path, "--key", writeReadableKey(t)); err == nil {
		t.Fatal("an oversized bundle was accepted")
	}
}

func TestVerifyCommandRejectsWorldReadableKey(t *testing.T) {
	body, err := os.ReadFile(verifyFixtureKey)
	if err != nil {
		t.Fatalf("read fixture key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "public.txt")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod key: %v", err)
	}
	if _, err := runVerify(t, "--bundle", verifyFixtureBundle, "--key", path); err == nil {
		t.Fatal("a world-readable key was accepted")
	}
}

func TestVerifyCommandRejectsMismatchedKey(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	body := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "other.txt")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod key: %v", err)
	}
	if _, err := runVerify(t, "--bundle", verifyFixtureBundle, "--key", path); err == nil {
		t.Fatal("a mismatched public key verified the fixture bundle")
	}
}
