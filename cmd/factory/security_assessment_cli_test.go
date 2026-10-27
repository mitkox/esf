package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/factory"
)

func executorConfigFile(t *testing.T, body string) (string, string) {
	t.Helper()
	name := filepath.Join(t.TempDir(), "executor.json")
	if err := os.WriteFile(name, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(body))
	return name, hex.EncodeToString(digest[:])
}

func TestSecurityAssessmentNameFromRunSpec(t *testing.T) {
	for _, format := range []string{"json", "toml"} {
		t.Run(format, func(t *testing.T) {
			body := `{"run_id":"assessment-run","repository":"https://github.com/acme/x","revision":"abc","task":"fix it","agent_harness":"opencode2","verification_profile":"default","security_assessment_profile":"tenant-api"}`
			if format == "toml" {
				body = "run_id = 'assessment-run'\nrepository = 'https://github.com/acme/x'\nrevision = 'abc'\ntask = 'fix it'\nagent_harness = 'opencode2'\nverification_profile = 'default'\nsecurity_assessment_profile = 'tenant-api'\n"
			}
			spec, err := readRunSpec(writeSpec(t, "run."+format, body))
			if err != nil {
				t.Fatal(err)
			}
			req, err := runRequestFromSpec(specConfig(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if req.SecurityAssessmentProfile != "tenant-api" {
				t.Fatalf("assessment profile = %q", req.SecurityAssessmentProfile)
			}
		})
	}
	path := "unused"
	cmd := newRunCommand(&path)
	if err := cmd.ParseFlags([]string{"--security-assessment", "tenant-api"}); err != nil {
		t.Fatal(err)
	}
	if value, _ := cmd.Flags().GetString("security-assessment"); value != "tenant-api" {
		t.Fatalf("assessment flag = %q", value)
	}
	if cmd.Flags().Lookup("security-executor-config") != nil {
		t.Fatal("run command exposes operator executor configuration")
	}
}

func TestWorkerSecurityExecutorRequiresPairedFlags(t *testing.T) {
	for _, flags := range [][]string{
		{"--security-executor-config", "/operator/executor.json"},
		{"--security-executor-sha256", strings.Repeat("a", 64)},
	} {
		configPath := filepath.Join(t.TempDir(), "missing.toml")
		cmd := newWorkerCommand(&configPath)
		cmd.SetArgs(flags)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "required together") {
			t.Fatalf("partial operator flags: %v", err)
		}
	}
	if executor, err := loadSecurityExecutor(factory.Config{}, "", ""); err != nil || executor != nil {
		t.Fatalf("absent operator flags enabled executor: executor=%v err=%v", executor, err)
	}
}

func TestSecurityExecutorSpecRejectsTamperingAndControlOverrides(t *testing.T) {
	for _, body := range []string{
		`{"cube":{"api_url":"https://attacker.invalid"}}`,
		`{"api_key":"sensitive-test-marker"}`,
		`{"endpoint_token":"sensitive-test-marker"}`,
		`{"guard_socket":"/guard.sock"} {}`,
		`null`,
		`[]`,
		`   `,
		strings.Repeat(" ", maxSecurityExecutorConfigBytes+1),
	} {
		path, digest := executorConfigFile(t, body)
		if _, err := readSecurityExecutorSpec(path, digest); err == nil {
			t.Fatal("accepted invalid or control-plane override configuration")
		} else if strings.Contains(err.Error(), "sensitive-test-marker") || strings.Contains(err.Error(), body) {
			t.Fatal("error exposed configuration content")
		}
	}
	path, digest := executorConfigFile(t, `{"guard_socket":"/guard.sock"}`)
	if err := os.WriteFile(path, []byte(`{"guard_socket":"/changed.sock"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecurityExecutorSpec(path, digest); err == nil || !strings.Contains(err.Error(), "pin mismatch") {
		t.Fatalf("accepted changed configuration: %v", err)
	}
	if _, err := readSecurityExecutorSpec("relative.json", digest); err == nil {
		t.Fatal("accepted relative operator path")
	}
	if _, err := readSecurityExecutorSpec(path, strings.ToUpper(digest)); err == nil {
		t.Fatal("accepted noncanonical digest")
	}
}

func TestSecurityExecutorLoadsOnlyExplicitPinnedOperatorConfig(t *testing.T) {
	dir := t.TempDir()
	driver := []byte("pinned driver fixture")
	driverPath := filepath.Join(dir, "assessment-driver")
	if err := os.WriteFile(driverPath, driver, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(driver)
	spec := securityExecutorSpec{
		GuardSocket:            filepath.Join(dir, "guard.sock"),
		DriverBinaryPath:       driverPath,
		DriverSHA256:           hex.EncodeToString(digest[:]),
		TargetEntrypoint:       "testdata/security-assessment/tenant-api/server.py",
		PrivateJournalRoot:     filepath.Join(dir, "journals"),
		PreviewCAFile:          filepath.Join(dir, "preview-ca.pem"),
		RuntimeInventorySHA256: strings.Repeat("b", 64),
	}
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path, pin := executorConfigFile(t, string(body))
	cfg := specConfig()
	cfg.Cube.Version = "0.7.3"
	cfg.Cube.ProxyNodeIP = "192.0.2.4"
	cfg.Cube.ProxyPortHTTP = 9443
	cfg.Cube.ProxyScheme = "https"
	if executor, err := loadSecurityExecutor(cfg, path, pin); err != nil || executor == nil {
		t.Fatalf("pinned operator configuration rejected: %v", err)
	}
	// The executor must consume the real operator Cube configuration. An invalid
	// trusted configuration cannot be repaired through the executor JSON file.
	cfg.Cube.APIURL = ""
	if _, err := loadSecurityExecutor(cfg, path, pin); err == nil {
		t.Fatal("executor ignored trusted Cube configuration")
	}
}
