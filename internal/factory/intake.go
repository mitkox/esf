package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/tomlx"
)

const maxIntakeOutput = 16 << 10

var intakeDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// IntakeConfig controls the optional host-side DSPy decision process.
type IntakeConfig struct {
	Enabled          bool           `toml:"enabled"`
	PythonExecutable string         `toml:"python_executable"`
	KeyFile          string         `toml:"key_file"`
	Model            string         `toml:"model"`
	Timeout          tomlx.Duration `toml:"timeout"`
	ProgramPath      string         `toml:"program_path"`
}

func (c IntakeConfig) effectiveModel() string {
	if c.Model == "" {
		return "jev-latest"
	}
	return c.Model
}

func (c IntakeConfig) effectiveTimeout() time.Duration {
	if c.Timeout == 0 {
		return 15 * time.Second
	}
	return c.Timeout.Std()
}

func (c IntakeConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if !filepath.IsAbs(c.PythonExecutable) || !filepath.IsAbs(c.KeyFile) {
		return errors.New("python_executable and key_file must be absolute paths")
	}
	if c.ProgramPath != "" && !filepath.IsAbs(c.ProgramPath) {
		return errors.New("program_path must be an absolute path")
	}
	if strings.TrimSpace(c.effectiveModel()) == "" || strings.ContainsAny(c.effectiveModel(), "\r\n") {
		return errors.New("model must be a nonempty single line")
	}
	if c.effectiveTimeout() <= 0 || c.effectiveTimeout() > time.Minute {
		return errors.New("timeout must be positive and at most one minute")
	}
	return nil
}

// IntakeResult is advice, never authority over a run or a verification gate.
type IntakeResult struct {
	Status     string         `json:"status"`
	Ready      *IntakeBoolean `json:"ready,omitempty"`
	TaskType   *IntakeChoice  `json:"task_type,omitempty"`
	Ambiguity  *IntakeScore   `json:"ambiguity,omitempty"`
	Model      string         `json:"model,omitempty"`
	Program    string         `json:"program,omitempty"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	ErrorCode  string         `json:"error_code,omitempty"`
}

type IntakeBoolean struct {
	Value       bool    `json:"value"`
	Probability float64 `json:"probability"`
	Confidence  float64 `json:"confidence"`
}

type IntakeChoice struct {
	Value         string             `json:"value"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type IntakeScore struct {
	Value         float64            `json:"value"`
	Level         string             `json:"level"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

func (r IntakeResult) valid() bool {
	if r.Status != "ok" || r.Ready == nil || r.TaskType == nil || r.Ambiguity == nil || (r.Program != "intake-v1" && !intakeDigestPattern.MatchString(r.Program)) {
		return false
	}
	if !unit(r.Ready.Probability) || !unit(r.Ready.Confidence) || !unit(r.TaskType.Confidence) || !unit(r.Ambiguity.Confidence) || r.Ambiguity.Value < 0 || r.Ambiguity.Value > 2 || math.IsNaN(r.Ambiguity.Value) || math.IsInf(r.Ambiguity.Value, 0) {
		return false
	}
	if !oneOf(r.TaskType.Value, "bugfix", "feature", "refactor", "docs", "other") || !oneOf(r.Ambiguity.Level, "clear", "partial", "ambiguous") {
		return false
	}
	return probabilitiesValid(r.TaskType.Probabilities, []string{"bugfix", "feature", "refactor", "docs", "other"}) && probabilitiesValid(r.Ambiguity.Probabilities, []string{"clear", "partial", "ambiguous"})
}

func unit(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }
func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
func probabilitiesValid(values map[string]float64, keys []string) bool {
	if len(values) != len(keys) {
		return false
	}
	sum := 0.0
	for _, key := range keys {
		value, ok := values[key]
		if !ok || !unit(value) {
			return false
		}
		sum += value
	}
	return math.Abs(sum-1) <= 0.02
}

// AssessIntake executes the decision client on the worker host. Only task text
// enters stdin; the credential stays in a local file and never enters Temporal.
func (a *Activities) AssessIntake(ctx context.Context, in IntakeInput) (IntakeResult, error) {
	cfg := a.cfg.Intake
	if !cfg.Enabled {
		return IntakeResult{Status: "disabled"}, nil
	}
	started := time.Now()
	unavailable := func(code string) (IntakeResult, error) {
		return IntakeResult{Status: "unavailable", Model: cfg.effectiveModel(), DurationMS: time.Since(started).Milliseconds(), ErrorCode: code}, nil
	}
	if _, err := os.Stat(cfg.KeyFile); err != nil {
		return unavailable("credential_unavailable")
	}
	input, err := json.Marshal(map[string]string{"task": in.Task})
	if err != nil {
		return unavailable("input_encoding")
	}
	privateHome, err := os.MkdirTemp("", "esf-intake-*")
	if err != nil {
		return unavailable("temporary_storage")
	}
	defer os.RemoveAll(privateHome)
	deadline, cancel := context.WithTimeout(ctx, cfg.effectiveTimeout())
	defer cancel()
	cmd := exec.CommandContext(deadline, cfg.PythonExecutable, "-m", "esf_intake", "assess")
	cmd.Env = []string{
		"PATH=/usr/bin:/bin", "PYTHONNOUSERSITE=1",
		"HOME=" + privateHome, "XDG_CACHE_HOME=" + privateHome,
		"TYPESAFE_API_KEY_FILE=" + cfg.KeyFile,
		"ESF_INTAKE_MODEL=" + cfg.effectiveModel(),
		"ESF_INTAKE_PROGRAM=" + cfg.ProgramPath,
	}
	cmd.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	cmd.Stdout = &boundedWriter{writer: &output, limit: maxIntakeOutput}
	cmd.Stderr = io.Discard // Provider errors can contain request or credential data.
	if err := cmd.Run(); err != nil {
		if deadline.Err() != nil {
			return unavailable("timeout")
		}
		return unavailable("process_failed")
	}
	// Decode into a whitelist so the subprocess cannot smuggle arbitrary fields
	// into Temporal history or artifacts, even if it prints valid decision data.
	var response struct {
		Status    string         `json:"status"`
		Ready     *IntakeBoolean `json:"ready"`
		TaskType  *IntakeChoice  `json:"task_type"`
		Ambiguity *IntakeScore   `json:"ambiguity"`
		Program   string         `json:"program"`
	}
	decoder := json.NewDecoder(&output)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil || decoder.Decode(new(any)) != io.EOF {
		return unavailable("invalid_response")
	}
	result := IntakeResult{
		Status: response.Status, Ready: response.Ready, TaskType: response.TaskType,
		Ambiguity: response.Ambiguity, Program: response.Program,
		Model: cfg.effectiveModel(), DurationMS: time.Since(started).Milliseconds(),
	}
	if !result.valid() {
		return unavailable("invalid_response")
	}
	return result, nil
}

type IntakeInput struct {
	Task string `json:"task"`
}

type boundedWriter struct {
	writer *bytes.Buffer
	limit  int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.writer.Len() {
		return 0, fmt.Errorf("intake output exceeds %d bytes", w.limit)
	}
	return w.writer.Write(p)
}
