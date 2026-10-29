// Command security-assessment is the fixed assessor entrypoint. Execute it
// inside the dedicated Cube VM; the factory owns allocation, transfer and teardown.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	assessment "github.com/mitkox/esf/internal/securityassessment"
)

//go:embed resolver.py
var resolverLauncher string

func endpointMapping(target, address string) (string, string, string, error) {
	endpoint, err := url.Parse(target)
	ip := net.ParseIP(address)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || ip == nil {
		return "", "", "", errors.New("fixed resolver requires exact HTTPS endpoint and numeric IP")
	}
	port := endpoint.Port()
	if port == "" {
		port = "443"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", "", "", errors.New("fixed endpoint port invalid")
	}
	return endpoint.Hostname(), strconv.Itoa(n), ip.String(), nil
}

func writePrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func main() {
	inputPath := flag.String("input", "", "private versioned input uploaded by factory")
	outputPath := flag.String("output", "", "fresh private /tmp output directory")
	endpointAddress := flag.String("endpoint-address", "", "trusted controller numeric IP for the exact HTTPS endpoint")
	flag.Parse()
	if err := run(*inputPath, *outputPath, *endpointAddress); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inputPath, outputPath, endpointAddress string) error {
	if !filepath.IsAbs(inputPath) || !filepath.IsAbs(outputPath) || !strings.HasPrefix(filepath.Clean(inputPath), "/tmp/") || !strings.HasPrefix(filepath.Clean(outputPath), "/tmp/") {
		return errors.New("private input/output must be explicit /tmp paths")
	}
	file, err := os.Open(inputPath)
	if err != nil {
		return errors.New("private assessment input unavailable")
	}
	defer file.Close()
	var input assessment.RunInput
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return errors.New("private assessment input malformed")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("private assessment input has trailing data")
	}
	if err := input.Profile.Validate(); err != nil {
		return err
	}
	hostname, port, numericAddress, err := endpointMapping(input.Profile.TargetURL, endpointAddress)
	if err != nil {
		return err
	}
	if err := os.Mkdir(outputPath, 0700); err != nil {
		return errors.New("assessment output already exists or is unavailable")
	}
	logsRoot := filepath.Join(outputPath, "logs-input")
	if err := os.Mkdir(logsRoot, 0700); err != nil {
		return err
	}
	input.WorkspaceRoot = filepath.Join(outputPath, "workspace")
	if err := os.Mkdir(input.WorkspaceRoot, 0700); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	input.ReadLogs = func(ctx context.Context, correlation string) (assessment.SuppliedLogs, error) {
		ready, _ := json.Marshal(map[string]any{"schema": "esf.fixed-assessment-ready/v1", "run_id": input.Identity.RunID, "target_instance_id": input.Identity.TargetInstanceID, "request_digest": input.Identity.RequestDigest, "profile_digest": input.Identity.ProfileDigest, "isolation_correlation": correlation})
		if err := writePrivate(filepath.Join(outputPath, "ready.json"), append(ready, '\n')); err != nil {
			return assessment.SuppliedLogs{}, errors.New("assessment checkpoint could not be retained")
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			meta, err := os.ReadFile(filepath.Join(logsRoot, "metadata.json"))
			if err == nil {
				var value struct {
					Schema   string `json:"schema"`
					SHA256   string `json:"sha256"`
					Bytes    int64  `json:"bytes"`
					Complete bool   `json:"complete"`
				}
				if len(meta) > 4096 || json.Unmarshal(meta, &value) != nil || value.Schema != "esf.supplied-application-logs/v1" || value.Bytes <= 0 || value.Bytes > input.Profile.Limits.MaxLogBytes || !value.Complete {
					return assessment.SuppliedLogs{}, errors.New("supplied log metadata incomplete")
				}
				f, err := os.Open(filepath.Join(logsRoot, "application.jsonl"))
				if err != nil {
					return assessment.SuppliedLogs{}, errors.New("supplied log data missing")
				}
				data, readErr := io.ReadAll(io.LimitReader(f, input.Profile.Limits.MaxLogBytes+1))
				closeErr := f.Close()
				if readErr != nil || closeErr != nil || int64(len(data)) != value.Bytes || assessment.VerifyEvidence(data, value.SHA256) != nil {
					return assessment.SuppliedLogs{}, errors.New("supplied log copy identity mismatch")
				}
				return assessment.SuppliedLogs{Bytes: data, Complete: true, ExpectedSHA256: value.SHA256}, nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return assessment.SuppliedLogs{}, errors.New("supplied log metadata unavailable")
			}
			select {
			case <-ctx.Done():
				return assessment.SuppliedLogs{}, errors.New("supplied logs did not arrive before deadline")
			case <-ticker.C:
			}
		}
	}
	command := exec.CommandContext(ctx, "/opt/security-runtime/venv/bin/python", "-I", "-c", resolverLauncher, hostname, port, numericAddress)
	command.Env = []string{"PATH=/opt/security-runtime/venv/bin:/usr/local/bin:/usr/bin:/bin", "HOME=" + input.WorkspaceRoot, "PYTHONNOUSERSITE=1", "PYTHONUNBUFFERED=1",
		"OPENCYBER_WORKSPACE=" + input.WorkspaceRoot, "OPENCYBER_ENFORCE_SCOPE=true", "OPENCYBER_RESULT_RETENTION=full", "REQUESTS_CA_BUNDLE=/tmp/route-ca.pem"}
	command.Stderr = io.Discard
	stdout, err := command.StdoutPipe()
	if err != nil {
		return errors.New("backend stdout unavailable")
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return errors.New("backend stdin unavailable")
	}
	if err := command.Start(); err != nil {
		return errors.New("pinned backend could not start")
	}
	client := assessment.NewStdioMCP(stdout, stdin)
	defer func() { client.Close(); command.Process.Kill(); command.Wait() }()
	if err := client.Initialize(ctx, input.Profile.RequiredTools); err != nil {
		failure, _ := json.Marshal(map[string]string{"schema": "esf.fixed-assessment-failure/v1", "code": "backend-initialize-incomplete"})
		_ = writePrivate(filepath.Join(outputPath, "incomplete.json"), failure)
		return errors.New("pinned backend initialization incomplete")
	}
	output, runErr := assessment.RunFixedAssessment(ctx, input, client)
	for name, data := range output.PrivateEvidence {
		if filepath.Base(name) != name {
			return errors.New("invalid driver evidence path")
		}
		if err := writePrivate(filepath.Join(outputPath, name), data); err != nil {
			return errors.New("selected evidence retention failed")
		}
	}
	result, _ := json.MarshalIndent(output, "", "  ")
	if err := writePrivate(filepath.Join(outputPath, "result.json"), append(result, '\n')); err != nil {
		return errors.New("assessment manifest retention failed")
	}
	if runErr != nil {
		return errors.New("assessment incomplete; inspect private result code")
	}
	return nil
}
