package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/evidence"
	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/httpsecurity"
	"github.com/mitkox/esf/internal/releaseinfo"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/secretfile"
	"github.com/spf13/cobra"
)

type dependencyVersion struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

// buildCommit is set by the release build, which uses -buildvcs=false for
// reproducible cross-platform binaries.
var buildCommit string

func newVersionCommand() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "version", Short: "Show ESF build and dependency versions", RunE: func(cmd *cobra.Command, _ []string) error {
		info := struct {
			Release      string              `json:"release"`
			Commit       string              `json:"commit"`
			GoVersion    string              `json:"go_version"`
			ConfigSchema int                 `json:"config_schema"`
			Dependencies []dependencyVersion `json:"dependencies"`
			Inventory    json.RawMessage     `json:"inventory"`
		}{Release: factory.Version, GoVersion: runtime.Version(), ConfigSchema: factory.ConfigSchemaVersion, Dependencies: []dependencyVersion{}, Inventory: releaseinfo.Inventory}
		if build, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range build.Settings {
				if setting.Key == "vcs.revision" {
					info.Commit = setting.Value
				}
			}
			for _, dep := range build.Deps {
				version := dep.Version
				if dep.Replace != nil {
					version = dep.Replace.Version
				}
				info.Dependencies = append(info.Dependencies, dependencyVersion{Path: dep.Path, Version: version})
			}
			sort.Slice(info.Dependencies, func(i, j int) bool { return info.Dependencies[i].Path < info.Dependencies[j].Path })
		}
		if buildCommit != "" {
			info.Commit = buildCommit
		}
		if asJSON {
			return printJSON(info)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ESF %s (commit %s, %s, config schema %d)\n", info.Release, info.Commit, info.GoVersion, info.ConfigSchema)
		return nil
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable release inventory")
	return cmd
}

func newConfigCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Validate or migrate factory configuration"}
	cmd.AddCommand(&cobra.Command{Use: "validate", RunE: func(_ *cobra.Command, _ []string) error {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		fmt.Println("factory configuration is valid")
		return nil
	}})
	var dryRun, apply bool
	migrate := &cobra.Command{Use: "migrate", Short: "Upgrade configuration schema while preserving comments", RunE: func(_ *cobra.Command, _ []string) error {
		if dryRun && apply {
			return fmt.Errorf("choose either --dry-run or --apply")
		}
		if !dryRun && !apply {
			return fmt.Errorf("specify --dry-run or --apply")
		}
		path := resolveConfigPath(*configPath)
		if path == "" {
			return fmt.Errorf("config migration requires an existing config file")
		}
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(original) > 1<<20 {
			return fmt.Errorf("config file exceeds 1 MiB")
		}
		updated, changed, err := migrateConfigText(string(original))
		if err != nil {
			return err
		}
		if !changed {
			fmt.Println("configuration schema is current")
			return nil
		}
		if dryRun {
			fmt.Println("migration available: add schema_version = 1 and remove legacy factory_version; no file changed")
			return nil
		}
		backup := path + ".pre-v1.backup"
		if _, err := os.Stat(backup); err == nil {
			return fmt.Errorf("backup %s already exists", backup)
		}
		if err := os.WriteFile(backup, original, 0o600); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".factory-config-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(updated); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Chmod(tmp.Name(), 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			return err
		}
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return err
		}
		fmt.Printf("migrated %s; backup: %s\n", path, backup)
		return nil
	}}
	migrate.Flags().BoolVar(&dryRun, "dry-run", false, "show the migration without changing files")
	migrate.Flags().BoolVar(&apply, "apply", false, "back up and replace the configuration")
	cmd.AddCommand(migrate)
	return cmd
}

var schemaLine = regexp.MustCompile(`(?m)^schema_version\s*=\s*([0-9]+)\s*$`)
var factoryVersionLine = regexp.MustCompile(`(?m)^factory_version\s*=\s*[^\n]*\n?`)

func migrateConfigText(original string) (string, bool, error) {
	if match := schemaLine.FindStringSubmatch(original); len(match) > 0 {
		if match[1] == "1" {
			return original, false, nil
		}
		return "", false, fmt.Errorf("unsupported configuration schema_version %s", match[1])
	}
	return "schema_version = 1\n" + factoryVersionLine.ReplaceAllString(original, ""), true, nil
}

func newAgentsCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{Use: "agents", Short: "Inspect installed agents"}
	cmd.AddCommand(&cobra.Command{Use: "verify", Short: "Verify configured host and Cube-template agent binary digests", RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return err
		}
		if err := cfg.Validate(); err != nil {
			return err
		}
		var templateSandbox sandbox.Sandbox
		var templateProvider sandbox.Provider
		defer func() {
			if templateSandbox == nil {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err := templateProvider.Destroy(cleanupCtx, templateSandbox.ID()); err != nil {
				runErr = fmt.Errorf("agent verification sandbox cleanup: %w; verification: %v", err, runErr)
			}
		}()
		names := make([]string, 0, len(cfg.Harnesses))
		for name := range cfg.Harnesses {
			names = append(names, name)
		}
		sort.Strings(names)
		failures := 0
		for _, name := range names {
			h := cfg.Harnesses[name]
			if strings.TrimSpace(h.Binary) == "" {
				continue
			}
			if len(h.BinarySHA256) != 64 {
				fmt.Printf("[FAIL] %s: binary_sha256 is required\n", name)
				failures++
				continue
			}
			if h.Preinstalled {
				if templateSandbox == nil {
					provider, err := cube.New(cfg.Cube)
					if err != nil {
						return err
					}
					templateProvider = provider
					verifyCtx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
					defer cancel()
					allowInternet := false
					templateSandbox, err = provider.Create(verifyCtx, sandbox.Spec{
						Metadata:    map[string]string{"origin": "esf", "purpose": "agent-verification"},
						IdleTimeout: 5 * time.Minute,
						Network:     sandbox.Network{AllowInternet: &allowInternet},
					})
					if err != nil {
						return fmt.Errorf("create template verification sandbox: %w", err)
					}
				}
				exec, err := templateSandbox.Execute(cmd.Context(), sandbox.Command{Argv: []string{"sha256sum", h.Binary}, Timeout: 30 * time.Second, Description: "verify agent in Cube template"})
				if err != nil || !exec.Succeeded() || len(strings.Fields(exec.Stdout)) == 0 || !strings.EqualFold(strings.Fields(exec.Stdout)[0], h.BinarySHA256) {
					fmt.Printf("[FAIL] %s: template binary digest mismatch or unreadable file\n", name)
					failures++
					continue
				}
				fmt.Printf("[ok] %s: template sha256 %s\n", name, h.BinarySHA256)
				if strings.EqualFold(h.Type, "pi") {
					registry, err := cfg.BuildHarnesses()
					if err != nil {
						return err
					}
					harness, err := registry.Resolve(name)
					if err != nil {
						return err
					}
					if err := harness.Provision(cmd.Context(), templateSandbox); err != nil {
						fmt.Printf("[FAIL] %s: execution chain: %v\n", name, err)
						failures++
					} else {
						fmt.Printf("[ok] %s: runtime digest and runner self-check\n", name)
					}
				}
				continue
			}
			file, err := os.Open(h.Binary)
			if err != nil {
				fmt.Printf("[FAIL] %s: %v\n", name, err)
				failures++
				continue
			}
			actual := sha256.New()
			_, err = io.Copy(actual, file)
			file.Close()
			if err != nil || !strings.EqualFold(hex.EncodeToString(actual.Sum(nil)), h.BinarySHA256) {
				fmt.Printf("[FAIL] %s: binary digest mismatch or unreadable file\n", name)
				failures++
				continue
			}
			fmt.Printf("[ok] %s: sha256 %s\n", name, h.BinarySHA256)
		}
		if failures > 0 {
			return fmt.Errorf("%d agent verification failure(s)", failures)
		}
		return nil
	}})
	return cmd
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

const (
	doctorMaxAPITokenBytes   = 4096
	doctorMaxSigningKeyBytes = 16 << 10
)

func passedCheck(name string) doctorCheck {
	return doctorCheck{Name: name, Status: "ok"}
}

func failedCheck(name string, err error) doctorCheck {
	return doctorCheck{Name: name, Status: "fail", Detail: err.Error()}
}

func skippedCheck(name, detail string) doctorCheck {
	return doctorCheck{Name: name, Status: "skipped", Detail: detail}
}

// productionAPIListener checks the execution API posture without opening a
// socket. It reads the bearer token through the private-file rules so a
// group-readable or malformed token is reported before the API pod starts.
func productionAPIListener(cfg factory.Config) doctorCheck {
	const name = "api_listener"
	if !cfg.API.Enabled {
		return skippedCheck(name, "api.enabled is false")
	}
	tlsConfig, err := httpsecurity.LoadTLSConfig(cfg.API.TLSCertFile, cfg.API.TLSKeyFile)
	if err != nil {
		return failedCheck(name, fmt.Errorf("api: %w", err))
	}
	if err := httpsecurity.ValidateListen(cfg.API.Listen, tlsConfig != nil); err != nil {
		return failedCheck(name, fmt.Errorf("api: %w", err))
	}
	body, err := secretfile.Read(cfg.API.TokenFile, doctorMaxAPITokenBytes)
	if err != nil {
		return failedCheck(name, fmt.Errorf("api.token_file: %w", err))
	}
	token := strings.TrimSpace(string(body))
	if len(token) < 32 || !httpsecurity.ValidBearerToken(token) {
		return failedCheck(name, fmt.Errorf("api.token_file: the bearer token must contain at least 32 characters from the ASCII bearer token alphabet"))
	}
	return passedCheck(name)
}

// productionConnectorConfiguration reads each connector through the same path
// the worker uses, so an invalid kind, base URL, or private token file fails
// the production posture check.
func productionConnectorConfiguration(cfg factory.Config) doctorCheck {
	const name = "connector_configuration"
	if len(cfg.Connectors) == 0 {
		return skippedCheck(name, "no connectors configured")
	}
	names := make([]string, 0, len(cfg.Connectors))
	for connectorName := range cfg.Connectors {
		names = append(names, connectorName)
	}
	sort.Strings(names)
	var problems []string
	for _, connectorName := range names {
		if _, err := cfg.Connector(connectorName); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		connectorConfig := cfg.Connectors[connectorName]
		tlsConfig, err := httpsecurity.LoadTLSConfig(connectorConfig.WebhookTLSCertFile, connectorConfig.WebhookTLSKeyFile)
		if err != nil {
			problems = append(problems, fmt.Sprintf("connector %q: webhook TLS: %v", connectorName, err))
			continue
		}
		if err := httpsecurity.ValidateListen(connectorConfig.WebhookListenAddress(), tlsConfig != nil); err != nil {
			problems = append(problems, fmt.Sprintf("connector %q: webhook_listen: %v", connectorName, err))
		}
	}
	if len(problems) > 0 {
		return failedCheck(name, errors.New(strings.Join(problems, "; ")))
	}
	return passedCheck(name)
}

// productionTokenFiles checks every bearer token the appliance reads: the API
// token and each connector token. Reading through the shared private-file
// rules rejects symlinks, special files, and group/other access.
func productionTokenFiles(cfg factory.Config) doctorCheck {
	const name = "token_file_permissions"
	var problems []string
	checked := 0
	if cfg.API.Enabled {
		checked++
		if _, err := secretfile.Read(cfg.API.TokenFile, doctorMaxAPITokenBytes); err != nil {
			problems = append(problems, fmt.Sprintf("api.token_file: %v", err))
		}
	}
	names := make([]string, 0, len(cfg.Connectors))
	for connectorName := range cfg.Connectors {
		names = append(names, connectorName)
	}
	sort.Strings(names)
	for _, connectorName := range names {
		checked++
		if _, err := forge.ReadCredential(cfg.Connectors[connectorName].TokenFile); err != nil {
			problems = append(problems, fmt.Sprintf("connector %q token_file: %v", connectorName, err))
		}
	}
	if checked == 0 {
		return skippedCheck(name, "no token files configured")
	}
	if len(problems) > 0 {
		return failedCheck(name, errors.New(strings.Join(problems, "; ")))
	}
	return passedCheck(name)
}

// productionEvidenceSigningKey requires the host-held signing key publication
// depends on and verifies it is a readable, private, PKCS#8 ed25519 key.
func productionEvidenceSigningKey(cfg factory.Config) doctorCheck {
	const name = "evidence_signing_key"
	path := strings.TrimSpace(cfg.Evidence.SigningKeyFile)
	if path == "" {
		return failedCheck(name, fmt.Errorf("evidence.signing_key_file is required for production publication; mount a 0600 PKCS#8 ed25519 key outside every Cube sandbox"))
	}
	body, err := secretfile.Read(path, doctorMaxSigningKeyBytes)
	if err != nil {
		return failedCheck(name, fmt.Errorf("evidence.signing_key_file: %w", err))
	}
	if _, err := evidence.ParsePrivateKey(body); err != nil {
		return failedCheck(name, fmt.Errorf("evidence.signing_key_file: %w", err))
	}
	return passedCheck(name)
}

func runStructuredDoctor(ctx context.Context, configPath, profile, output string, offline bool) error {
	if profile != "" && profile != "production" {
		return fmt.Errorf("unsupported doctor profile %q", profile)
	}
	if output != "" && output != "json" {
		return fmt.Errorf("unsupported doctor output %q", output)
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	checks := []doctorCheck{}
	add := func(name string, err error) {
		c := doctorCheck{Name: name, Status: "ok"}
		if err != nil {
			c.Status = "fail"
			c.Detail = err.Error()
		}
		checks = append(checks, c)
	}
	add("configuration", cfg.Validate())
	if profile == "production" {
		checks = append(checks,
			productionAPIListener(cfg),
			productionConnectorConfiguration(cfg),
			productionTokenFiles(cfg),
			productionEvidenceSigningKey(cfg),
		)
		add("storage_local", checkSQLiteFilesystem(cfg.Storage.DataDir))
		if len(cfg.Sandbox.BasePackages) != 0 {
			add("sandbox_template_tools", fmt.Errorf("versioned Cube templates must provide prerequisites without sandbox.base_packages"))
		} else {
			add("sandbox_template_tools", nil)
		}
		if !cfg.Temporal.TLS {
			add("temporal_tls", fmt.Errorf("temporal.tls must be true"))
		} else {
			add("temporal_tls", nil)
		}
		if cfg.Cube.ProxyNodeIP != "" && cfg.Cube.ProxyScheme != "https" {
			add("cube_data_tls", fmt.Errorf("cube.proxy_scheme must be https"))
		} else {
			add("cube_data_tls", nil)
		}
		names := make([]string, 0, len(cfg.Harnesses))
		for name := range cfg.Harnesses {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			h := cfg.Harnesses[name]
			if h.Type == "opencode" || h.Type == "unreal" || h.Type == "pi" || h.Type == "claude" || h.Type == "codex" {
				var e error
				if !h.Preinstalled || len(h.Packages) != 0 {
					e = fmt.Errorf("use a digest-pinned agent in the versioned Cube template without per-run package installs")
				}
				if h.CredentialMode != "cube_egress" {
					e = fmt.Errorf("credential_mode must be cube_egress")
				}
				if len(h.BinarySHA256) != 64 {
					e = fmt.Errorf("binary_sha256 must pin the agent")
				}
				add("agent_"+name, e)
			}
		}
	}
	if offline {
		checks = append(checks, doctorCheck{Name: "connectivity", Status: "skipped", Detail: "--offline"})
	} else if cfg.Validate() == nil {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		runtime, err := factory.NewRuntime(probeCtx, factory.RuntimeOptions{Config: cfg})
		if err != nil {
			add("runtime", err)
		} else {
			defer runtime.Close(probeCtx)
			add("cube_reachable", runtime.Provider.Ping(probeCtx))
			client, err := runtime.TemporalClient()
			if err != nil {
				add("temporal_reachable", err)
			} else {
				defer client.Close()
				_, err = client.DescribeWorkflowExecution(probeCtx, "factory-doctor-probe", "")
				if err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
					add("temporal_reachable", err)
				} else {
					add("temporal_reachable", nil)
				}
			}
		}
	}
	result := struct {
		Profile string        `json:"profile"`
		Checks  []doctorCheck `json:"checks"`
		Passed  bool          `json:"passed"`
	}{Profile: profile, Checks: checks, Passed: true}
	for _, c := range checks {
		if c.Status == "fail" {
			result.Passed = false
		}
	}
	if output == "json" {
		if err := printJSON(result); err != nil {
			return err
		}
	} else {
		for _, c := range checks {
			fmt.Printf("[%s] %s %s\n", c.Status, c.Name, c.Detail)
		}
	}
	if !result.Passed {
		return fmt.Errorf("factory doctor found failed checks")
	}
	return nil
}
