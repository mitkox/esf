package factory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/evidence"
	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/verification"
)

func publicationGateConfig() Config {
	mandatory := true
	return Config{
		Verification: map[string]verification.Profile{
			"direct": {Name: "direct", Steps: []verification.Step{
				{ID: "build", Argv: []string{"./build.sh"}, Mandatory: &mandatory},
			}},
			"shell": {Name: "shell", Steps: []verification.Step{
				{ID: "shell-check", Argv: []string{"bash", "-c", "./scripts/check.sh --strict"}, Mandatory: &mandatory},
			}},
			"make": {Name: "make", Steps: []verification.Step{
				{ID: "make-test", Argv: []string{"make", "test"}, Mandatory: &mandatory},
			}},
			"package": {Name: "package", Steps: []verification.Step{
				{ID: "npm-test", Argv: []string{"npm", "run", "test"}, Mandatory: &mandatory},
			}},
			"fixtures": {Name: "fixtures", Steps: []verification.Step{
				{ID: "pytest", Argv: []string{"pytest", "testdata/fixture.json"}, Mandatory: &mandatory},
			}},
		},
	}
}

func TestPublicationGatesRejectProtectedPaths(t *testing.T) {
	t.Parallel()

	cfg := publicationGateConfig()
	protected := cfg.PublicationProtectedPaths()
	for _, want := range []string{"build.sh", "scripts/check.sh", "Makefile", "package.json", "testdata/fixture.json"} {
		if !containsString(protected, want) {
			t.Fatalf("PublicationProtectedPaths() = %v, missing %q", protected, want)
		}
	}

	cases := []struct {
		name  string
		path  string
		class PublicationViolationClass
	}{
		{name: "github workflow", path: ".github/workflows/ci.yml", class: PublicationViolationCI},
		{name: "gitlab ci", path: ".gitlab-ci.yml", class: PublicationViolationCI},
		{name: "azure pipelines", path: "azure-pipelines.yml", class: PublicationViolationCI},
		{name: "evidence directory", path: "evidence/bundle.json", class: PublicationViolationEvidence},
		{name: "direct gate", path: "build.sh", class: PublicationViolationVerification},
		{name: "shell reached script", path: "scripts/check.sh", class: PublicationViolationVerification},
		{name: "make target", path: "Makefile", class: PublicationViolationVerification},
		{name: "package script", path: "package.json", class: PublicationViolationVerification},
		{name: "test fixture", path: "testdata/fixture.json", class: PublicationViolationVerification},
		{name: "path traversal", path: "../outside", class: PublicationViolationPath},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := EvaluatePublicationGates(PublicationGateInput{
				ChangedPaths:   []string{tc.path},
				ProtectedPaths: protected,
			})
			if err != nil {
				t.Fatalf("EvaluatePublicationGates: %v", err)
			}
			if result.Passed {
				t.Fatalf("gate passed for %q", tc.path)
			}
			found := false
			for _, violation := range result.Violations {
				if violation.Class == tc.class {
					found = true
				}
			}
			if !found {
				t.Fatalf("violations = %#v, want class %q", result.Violations, tc.class)
			}
		})
	}
}

func TestPublicationGatesHonourStepWorkingDirectory(t *testing.T) {
	t.Parallel()
	cfg := Config{Verification: map[string]verification.Profile{
		"frontend": {Name: "frontend", Steps: []verification.Step{
			{ID: "npm-test", Dir: "frontend", Argv: []string{"npm", "run", "test"}},
		}},
	}}
	protected := cfg.PublicationProtectedPaths()
	if !containsString(protected, "frontend/package.json") {
		t.Fatalf("PublicationProtectedPaths() = %v, want frontend/package.json", protected)
	}
	for _, value := range protected {
		if strings.Contains(value, "frontend/frontend/") {
			t.Fatalf("step dir was applied twice: %v", protected)
		}
	}
}

func TestPublicationGatesAllowOrdinaryChange(t *testing.T) {
	t.Parallel()
	result, err := EvaluatePublicationGates(PublicationGateInput{
		ChangedPaths:   []string{"internal/payment/retry.go", "docs/readme.md"},
		ProtectedPaths: publicationGateConfig().PublicationProtectedPaths(),
	})
	if err != nil {
		t.Fatalf("EvaluatePublicationGates: %v", err)
	}
	if !result.Passed {
		t.Fatalf("ordinary change rejected: %#v", result.Violations)
	}
}

func TestPublicationGatesSubmoduleAllowlist(t *testing.T) {
	t.Parallel()
	baseline := []byte("[submodule \"shared\"]\n\tpath = shared\n\turl = https://dev.azure.com/acme/shared/_git/lib\n")
	changed := []byte("[submodule \"shared\"]\n\tpath = shared\n\turl = https://dev.azure.com/acme/shared/_git/lib2\n")

	result, err := EvaluatePublicationGates(PublicationGateInput{
		ChangedPaths:       []string{".gitmodules"},
		BaselineGitmodules: baseline,
		ResultGitmodules:   changed,
	})
	if err != nil {
		t.Fatalf("EvaluatePublicationGates: %v", err)
	}
	if result.Passed {
		t.Fatal("an unallowlisted submodule URL change passed")
	}

	allowed, err := EvaluatePublicationGates(PublicationGateInput{
		ChangedPaths:          []string{".gitmodules"},
		BaselineGitmodules:    baseline,
		ResultGitmodules:      changed,
		SubmoduleURLAllowlist: []string{"https://dev.azure.com/acme/shared/_git/lib", "https://dev.azure.com/acme/shared/_git/lib2"},
	})
	if err != nil {
		t.Fatalf("EvaluatePublicationGates allowlisted: %v", err)
	}
	if !allowed.Passed {
		t.Fatalf("allowlisted submodule change rejected: %#v", allowed.Violations)
	}
}

func TestPublicationGatesBaselineNotPatchText(t *testing.T) {
	t.Parallel()
	// A deleted protected file appears only as a removal in a patch. The tree
	// diff names it, so the gate still sees it.
	result, err := EvaluatePublicationGates(PublicationGateInput{
		ChangedPaths:   []string{".github/workflows/ci.yml"},
		ProtectedPaths: nil,
	})
	if err != nil {
		t.Fatalf("EvaluatePublicationGates: %v", err)
	}
	if result.Passed {
		t.Fatal("a deleted CI definition passed the gate")
	}
}

func TestEvidenceGateMatchesManifest(t *testing.T) {
	t.Parallel()
	keyPath := writePublicationSigningKey(t)
	cfg := Config{Evidence: EvidenceConfig{SigningKeyFile: keyPath}}
	keyBody, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := evidence.ParsePrivateKey(keyBody)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	completed := started.Add(5 * time.Minute)
	manifest := RunManifest{
		RunID: "run-publication", ChangeID: "change-publication",
		FactoryResult: StateSucceeded, BaselineSHA: strings.Repeat("a", 40),
		ResultingSHA: strings.Repeat("b", 40), TaskHash: strings.Repeat("c", 64),
		VerificationProfile: "default", VerificationResult: OutcomeSuccess,
		StartedAt: started, CompletedAt: completed,
	}
	patch := []byte("diff --git a/main.go b/main.go\n+package main\n")
	record := evidence.Record{
		RunID: manifest.RunID, ChangeID: manifest.ChangeID, State: string(StateSucceeded),
		StartedAt: started, CompletedAt: completed, Repository: "https://example.test/repo",
		BaseCommit: manifest.BaselineSHA, ResultCommit: manifest.ResultingSHA,
		TaskHash: manifest.TaskHash, Patch: patch,
		Verification: evidence.Verification{Profile: "default", Passed: true},
	}
	envelope, err := evidence.Build(record, evidence.Ed25519Signer{Key: privateKey})
	if err != nil {
		t.Fatal(err)
	}
	envelopeBody, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	factory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := factory.ForRun(manifest.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ArtifactEvidenceBundle, envelopeBody); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ArtifactPatch, patch); err != nil {
		t.Fatal(err)
	}
	activities := &Activities{cfg: cfg}
	digest, gates, err := activities.verifyEvidenceAgainstManifest(store, manifest)
	if err != nil {
		t.Fatalf("verifyEvidenceAgainstManifest: %v", err)
	}
	if !strings.HasPrefix(digest, "sha256:") || gates != 0 {
		t.Fatalf("digest/gates = %q/%d", digest, gates)
	}

	tampered := manifest
	tampered.TaskHash = strings.Repeat("d", 64)
	if _, _, err := activities.verifyEvidenceAgainstManifest(store, tampered); err == nil {
		t.Fatal("a bundle that does not match the manifest verified")
	}

	if err := store.Write(ArtifactPatch, []byte("diff --git a/other.go b/other.go\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := activities.verifyEvidenceAgainstManifest(store, manifest); err == nil {
		t.Fatal("a bundle whose patch subject does not match the durable patch verified")
	}
}

func TestSandboxEnvironmentExcludesSigningAndPublishingSecrets(t *testing.T) {
	t.Parallel()
	configDir := t.TempDir()
	keyPath := filepath.Join(configDir, "signing-key.pem")
	tokenPath := filepath.Join(configDir, "azure.token")
	cfg := Config{
		Evidence: EvidenceConfig{SigningKeyFile: keyPath},
		Connectors: map[string]ConnectorConfig{
			"azdo": {Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme", TokenFile: tokenPath},
		},
	}
	if err := cfg.AssertSandboxSecretIsolation(map[string]string{"PATH": "/usr/bin"}); err != nil {
		t.Fatalf("clean environment rejected: %v", err)
	}
	cases := []map[string]string{
		{"SIGNING_KEY": keyPath},
		{"SOME_PATH": tokenPath},
		{"AZURE_DEVOPS_EXT_PAT": "secret"},
		{"FILE_URL": "file://" + tokenPath},
	}
	for _, env := range cases {
		if err := cfg.AssertSandboxSecretIsolation(env); err == nil {
			t.Fatalf("environment %#v passed secret isolation", env)
		}
	}
	withSetupPath := cfg
	withSetupPath.Sandbox.SetupScript = "cat " + keyPath
	if err := withSetupPath.validateSecretIsolationConfig(); err == nil {
		t.Fatal("a sandbox setup script referencing the signing key path passed startup validation")
	}
}

func writePublicationSigningKey(t *testing.T) string {
	t.Helper()
	path, _ := writeSigningKey(t)
	return path
}
