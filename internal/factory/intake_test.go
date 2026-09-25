package factory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/tomlx"
)

const intakeFixture = `{"status":"ok","program":"intake-v1","ready":{"value":false,"probability":0.25,"confidence":0.5},"task_type":{"value":"bugfix","probabilities":{"bugfix":0.6,"feature":0.1,"refactor":0.1,"docs":0.1,"other":0.1},"confidence":0.6},"ambiguity":{"value":1.5,"level":"ambiguous","probabilities":{"clear":0.1,"partial":0.3,"ambiguous":0.6},"confidence":0.6}}`

func intakeTestConfig(t *testing.T, script string) IntakeConfig {
	t.Helper()
	dir := t.TempDir()
	executable := filepath.Join(dir, "fake-python")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte("sentinel-private-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return IntakeConfig{Enabled: true, PythonExecutable: executable, KeyFile: key, Model: "jev-latest", Timeout: tomlx.FromStd(time.Second)}
}

func TestAssessIntakeAdvisoryOutcomes(t *testing.T) {
	tests := []struct {
		name, script, wantStatus, wantError string
		timeout                             time.Duration
	}{
		{"decision", "cat >/dev/null\nprintf '%s' '" + intakeFixture + "'", "ok", "", time.Second},
		{"invalid response", "cat >/dev/null\nprintf 'not-json'", "unavailable", "invalid_response", time.Second},
		{"untrusted output", "cat >/dev/null\nprintf '%s' '" + strings.Replace(intakeFixture, "intake-v1", "sentinel-private-key", 1) + "'", "unavailable", "invalid_response", time.Second},
		{"extra field with credential", "cat >/dev/null\nprintf '%s' '" + strings.TrimSuffix(intakeFixture, "}") + `,"error_code":"sentinel-private-key"}` + "'", "unavailable", "invalid_response", time.Second},
		{"invalid probability distribution", "cat >/dev/null\nprintf '%s' '" + strings.Replace(intakeFixture, `"bugfix":0.6`, `"bugfix":0.9`, 1) + "'", "unavailable", "invalid_response", time.Second},
		{"score outside rubric", "cat >/dev/null\nprintf '%s' '" + strings.Replace(intakeFixture, `"value":1.5`, `"value":3.5`, 1) + "'", "unavailable", "invalid_response", time.Second},
		{"process error", "echo sentinel-private-key >&2\nexit 1", "unavailable", "process_failed", time.Second},
		{"timeout", "sleep 2", "unavailable", "timeout", 20 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := intakeTestConfig(t, tt.script)
			cfg.Timeout = tomlx.FromStd(tt.timeout)
			activity := &Activities{cfg: Config{Intake: cfg}}
			result, err := activity.AssessIntake(context.Background(), IntakeInput{Task: "fix the bug"})
			if err != nil || result.Status != tt.wantStatus || result.ErrorCode != tt.wantError {
				t.Fatalf("AssessIntake = %+v, %v", result, err)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "sentinel-private-key") {
				t.Fatal("credential leaked to activity result")
			}
		})
	}
}

func TestIntakeConfigIsOptInAndRequiresHostPaths(t *testing.T) {
	if Default().Intake.Enabled {
		t.Fatal("intake must be disabled by default")
	}
	path := filepath.Join(t.TempDir(), "factory.toml")
	body := "[intake]\nenabled = true\npython_executable = \"/opt/factory/venv/bin/python\"\nkey_file = \"/run/credentials/factory-worker.service/typesafe-api-key\"\ntimeout = \"12s\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil || !cfg.Intake.Enabled || cfg.Intake.effectiveTimeout() != 12*time.Second || cfg.Intake.Validate() != nil {
		t.Fatalf("loaded intake config = %+v, %v", cfg.Intake, err)
	}
	cfg.Intake.KeyFile = "relative/key"
	if cfg.Intake.Validate() == nil {
		t.Fatal("relative credential path was accepted")
	}
}

func TestIntakeDecisionIsRecordedWithoutBlockingRun(t *testing.T) {
	tests := []struct {
		name, response string
		ready          bool
	}{
		{"needs_clarification", intakeFixture, false},
		{"ready", strings.Replace(intakeFixture, `"value":false`, `"value":true`, 1), true},
		{"low_confidence", strings.Replace(intakeFixture, `"confidence":0.5`, `"confidence":0.01`, 1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := tt.response
			cfg := intakeTestConfig(t, "cat >/dev/null\nprintf '%s' '"+response+"'")
			fake := sandbox.NewFake()
			fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
			manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{mutateConfig: func(c *Config) {
				c.Intake = cfg
			}})
			if err != nil || manifest.FactoryResult != StateSucceeded {
				t.Fatalf("run = %s, %v", manifest.FactoryResult, err)
			}
			if manifest.Intake == nil || manifest.Intake.Status != "ok" || manifest.Intake.Ready.Value != tt.ready {
				t.Fatalf("intake result = %+v", manifest.Intake)
			}
		})
	}
}

func TestIntakeFailureDoesNotBlockRun(t *testing.T) {
	cfg := intakeTestConfig(t, "exit 1")
	artifactDir := t.TempDir()
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute(""))
	manifest, err := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{artifactDir: artifactDir, mutateConfig: func(c *Config) {
		c.Intake = cfg
	}})
	if err != nil || manifest.FactoryResult != StateSucceeded || manifest.Intake == nil || manifest.Intake.Status != "unavailable" {
		t.Fatalf("run = %+v, %v", manifest, err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(artifactDir, "runs", manifest.RunID, ArtifactRun))
	if err != nil || strings.Contains(string(manifestBytes), "sentinel-private-key") {
		t.Fatalf("manifest artifact leaked credential or could not be read: %v", err)
	}
}

func TestIntakeRunsBeforeSandboxCreation(t *testing.T) {
	cfg := intakeTestConfig(t, "cat >/dev/null\nprintf '%s' '"+intakeFixture+"'")
	fake := sandbox.NewFake()
	fake.CreateErr = errors.New("test create failure")
	manifest, _ := runWorkflowOpts(t, fake, baseRequest(t), workflowRunOptions{mutateConfig: func(c *Config) {
		c.Intake = cfg
	}})
	if manifest.Intake == nil || manifest.Intake.Status != "ok" || manifest.FactoryResult != StateInfrastructureFailed {
		t.Fatalf("intake should precede sandbox creation: %+v", manifest)
	}
}
