package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/securityassessment"
)

const maxSecurityExecutorConfigBytes = 64 << 10

// This operator-only format deliberately cannot override Cube control-plane
// addresses or credentials. Those always come from the factory configuration.
type securityExecutorSpec struct {
	GuardSocket            string `json:"guard_socket"`
	DriverBinaryPath       string `json:"driver_binary_path"`
	DriverSHA256           string `json:"driver_sha256"`
	TargetEntrypoint       string `json:"target_entrypoint"`
	PrivateJournalRoot     string `json:"private_journal_root"`
	PreviewCAFile          string `json:"preview_ca_file"`
	RuntimeInventorySHA256 string `json:"runtime_inventory_sha256"`
}

func validateSecurityExecutorFlags(configPath, expectedSHA256 string) error {
	if configPath == "" && expectedSHA256 == "" {
		return nil
	}
	if configPath == "" || expectedSHA256 == "" {
		return errors.New("--security-executor-config and --security-executor-sha256 are required together")
	}
	if !filepath.IsAbs(configPath) {
		return errors.New("security executor configuration path must be absolute")
	}
	digest, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != expectedSHA256 {
		return errors.New("security executor configuration SHA-256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func readSecurityExecutorSpec(configPath, expectedSHA256 string) (securityExecutorSpec, error) {
	var spec securityExecutorSpec
	if err := validateSecurityExecutorFlags(configPath, expectedSHA256); err != nil {
		return spec, err
	}
	f, err := os.Open(configPath)
	if err != nil {
		return spec, errors.New("security executor configuration unavailable")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSecurityExecutorConfigBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxSecurityExecutorConfigBytes {
		return spec, errors.New("security executor configuration exceeds bounds or cannot be read")
	}
	if securityassessment.SHA256(data) != expectedSHA256 {
		return spec, errors.New("security executor configuration pin mismatch")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return spec, errors.New("security executor configuration must contain one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return securityExecutorSpec{}, errors.New("invalid security executor configuration JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return securityExecutorSpec{}, errors.New("security executor configuration must contain one JSON object")
	}
	return spec, nil
}

func loadSecurityExecutor(cfg factory.Config, configPath, expectedSHA256 string) (factory.SecurityAssessmentExecutor, error) {
	if configPath == "" && expectedSHA256 == "" {
		return nil, nil
	}
	spec, err := readSecurityExecutorSpec(configPath, expectedSHA256)
	if err != nil {
		return nil, err
	}
	return factory.NewCubeSecurityAssessmentExecutor(securityassessment.CubeExecutorConfig{
		Cube:                   cfg.Cube,
		GuardSocket:            spec.GuardSocket,
		DriverBinaryPath:       spec.DriverBinaryPath,
		DriverSHA256:           spec.DriverSHA256,
		TargetEntrypoint:       spec.TargetEntrypoint,
		PrivateJournalRoot:     spec.PrivateJournalRoot,
		PreviewCAFile:          spec.PreviewCAFile,
		RuntimeInventorySHA256: spec.RuntimeInventorySHA256,
	})
}
