package securityassessment

import (
	"encoding/json"
	"strings"
	"testing"
)

func testIdentity() Identity {
	p := testProfile()
	profile, _ := p.Digest()
	return Identity{RunID: "run-a", ChangeID: "change-a", ParentRunID: "parent-a", CandidateDigest: strings.Repeat("1", 64), BuiltArtifactDigest: strings.Repeat("2", 64), TargetInstanceID: "target-a", ProfileDigest: profile, RequestDigest: strings.Repeat("3", 64), RequiredChecks: []string{"tenant-control", "tenant-isolation"}, Runtime: testRuntimePin(), Limits: DefaultLimits()}
}

func testManifest() Manifest {
	return Manifest{Schema: ManifestSchema, Identity: testIdentity(), Coverage: []Coverage{{ID: "tenant-control", Status: Complete}, {ID: "tenant-isolation", Status: Complete}}, Assertions: []Assertion{{ID: "tenant-control", Status: Passed}, {ID: "tenant-isolation", Status: Passed}}, Cleanup: Cleanup{Status: Complete}, Evidence: []EvidenceRef{{Name: "selected.json", SHA256: SHA256([]byte("evidence")), Bytes: 8}}, AI: AIOutcome{Status: "not-requested"}}
}

func TestAssessmentRequiresExternallyHeldEvidenceAndExactIdentity(t *testing.T) {
	m := testManifest()
	expected := testIdentity()
	output := map[string][]byte{"selected.json": []byte("evidence")}
	hashes := map[string]string{"selected.json": SHA256([]byte("evidence"))}
	if err := m.Verify(expected, output, hashes); err != nil {
		t.Fatal(err)
	}
	if !m.Passed() {
		t.Fatal("complete asserted evidence not passed")
	}
	changed := expected
	changed.CandidateDigest = strings.Repeat("4", 64)
	if m.Verify(changed, output, hashes) == nil {
		t.Fatal("different candidate accepted")
	}
	if m.Verify(expected, output, nil) == nil {
		t.Fatal("assessment self-supplied hash trusted")
	}
	output["selected.json"] = []byte("tampered")
	if m.Verify(expected, output, hashes) == nil {
		t.Fatal("tampered export accepted")
	}
}

func TestMissingPartialAndTruncatedEvidenceNeverPass(t *testing.T) {
	for _, mutate := range []func(*Manifest){
		func(m *Manifest) { m.Coverage = nil },
		func(m *Manifest) { m.Coverage[0].Status = Incomplete },
		func(m *Manifest) { m.Assertions = nil },
		func(m *Manifest) { m.Assertions[0].Status = Unobserved },
		func(m *Manifest) { m.Cleanup.Status = Incomplete },
		func(m *Manifest) { m.Evidence = nil },
		func(m *Manifest) { m.Truncated = true },
		func(m *Manifest) { m.Schema = "future" },
		func(m *Manifest) { m.Coverage = m.Coverage[:1]; m.Assertions = m.Assertions[:1] },
	} {
		m := testManifest()
		if err := m.Verify(testIdentity(), map[string][]byte{"selected.json": []byte("evidence")}, map[string]string{"selected.json": SHA256([]byte("evidence"))}); err != nil {
			t.Fatal(err)
		}
		mutate(&m)
		if m.Passed() {
			t.Fatalf("incomplete manifest reported pass: %+v", m)
		}
	}
}

func TestCompleteAssessmentCanContainConfirmedFailureAndUnavailableAI(t *testing.T) {
	m := testManifest()
	m.Assertions[1].Status = Failed
	m.Identity.AI = AIConfig{Enabled: true, Provider: "chosen-provider", Model: "chosen-model"}
	m.AI = AIOutcome{Status: "unavailable", Provider: "chosen-provider", Model: "chosen-model"}
	if err := m.Verify(m.Identity, map[string][]byte{"selected.json": []byte("evidence")}, map[string]string{"selected.json": SHA256([]byte("evidence"))}); err != nil {
		t.Fatal(err)
	}
	if m.Passed() {
		t.Fatal("confirmed failure passed")
	}
	encoded, _ := json.Marshal(m)
	if !strings.Contains(string(encoded), "chosen-model") {
		t.Fatal("configured model was substituted")
	}
}

func TestManifestDecodeIsStrictAndDoesNotCarryVerificationTrust(t *testing.T) {
	m := testManifest()
	if err := m.Verify(testIdentity(), map[string][]byte{"selected.json": []byte("evidence")}, map[string]string{"selected.json": SHA256([]byte("evidence"))}); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(m)
	decoded, err := DecodeManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Passed() {
		t.Fatal("deserialized record acquired controller verification trust")
	}
	for _, input := range [][]byte{[]byte(`{"schema":"future"}`), []byte(`{"schema":"esf.security-assessment/v1","unexpected":true}`), append(encoded, []byte("{}")...), encoded[:len(encoded)-1]} {
		if _, err := DecodeManifest(input); err == nil {
			t.Fatal("unknown or malformed manifest accepted")
		}
	}
}
