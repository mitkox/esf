package factory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/tomlx"
	"github.com/mitkox/esf/internal/verification"
)

// Version is the factory implementation version recorded in every run
// manifest. It is what makes "which factory produced this patch?" answerable.
const Version = "0.1.0"

// Config is the operator configuration for the whole factory.
//
// Everything in this structure is OPERATOR policy. A task or API caller can
// choose only: the task text, an approved repository, and an approved revision.
type Config struct {
	// FactoryVersion overrides Version in evidence when set.
	FactoryVersion string `toml:"factory_version"`

	Cube     cube.Config    `toml:"cube"`
	Temporal TemporalConfig `toml:"temporal"`
	Storage  StorageConfig  `toml:"storage"`
	Limits   LimitsConfig   `toml:"limits"`
	Sandbox  SandboxConfig  `toml:"sandbox"`

	// Harnesses are the agent registrations, keyed by harness name.
	Harnesses map[string]HarnessConfig `toml:"harnesses"`

	// Models are the named model endpoints a harness may use.
	Models map[string]ModelConfig `toml:"models"`

	// Egress holds the named network policies a run may execute under.
	Egress map[string]EgressPolicyConfig `toml:"egress"`

	// Workspaces are the named pre-warmed execution environments.
	Workspaces map[string]WorkspaceConfig `toml:"workspaces"`

	// Budgets are the named spending ceilings.
	Budgets map[string]BudgetConfig `toml:"budgets"`

	// Scopes are the tenancy boundaries. A caller names one; it can only
	// narrow the operator's global policy, never widen it.
	Scopes map[string]ScopeConfig `toml:"scopes"`

	// Review configures the human review gate.
	Review ReviewConfig `toml:"review"`

	// Verification holds the deterministic gate profiles.
	Verification map[string]verification.Profile `toml:"verification"`

	// Repositories constrains which remote repositories may be used.
	Repositories RepositoriesConfig `toml:"repositories"`

	Observability ObservabilityConfig `toml:"observability"`
}

// SandboxConfig controls factory-owned sandbox preparation.
type SandboxConfig struct {
	// BasePackages are installed in EVERY sandbox before the agent harness is
	// provisioned.
	//
	// These belong to the factory, not to a harness: repository preparation
	// needs git regardless of which agent runs, so making the requirement a
	// harness detail would leave the factory unable to clone for any harness
	// that forgot to declare it.
	BasePackages []string `toml:"base_packages"`

	// SetupScript is factory-owned shell code run after BasePackages and before
	// the agent harness is provisioned.
	//
	// It exists for toolchains a package manager cannot provide — a language
	// runtime's dependencies, for example. It is OPERATOR configuration and
	// must never contain task-derived text. It runs before the repository is
	// cloned, so it can only prepare the image, not the checkout.
	SetupScript string `toml:"setup_script"`

	// AllowAttach permits `factory attach`, which executes an operator-supplied
	// command inside a run's sandbox. It is DEFAULT CLOSED for the same reason a
	// debug port is: attach is remote code execution with the factory's
	// credentials, and it must be an explicit operator decision, never a
	// caller's. Every attach is audited in the run's evidence directory.
	AllowAttach bool `toml:"allow_attach"`

	// AllowPreview permits `factory preview`, which publishes a port from a
	// run's sandbox through the deployment ingress. Default closed.
	AllowPreview bool `toml:"allow_preview"`

	// RuntimeAllowOut lists additional destinations an agent may reach after
	// repository preparation. Egress-managed harnesses otherwise receive only
	// their configured model endpoint under a deny-by-default policy.
	RuntimeAllowOut []string `toml:"runtime_allow_out"`
}

// TemporalConfig locates the Temporal cluster.
type TemporalConfig struct {
	HostPort  string `toml:"host_port"`
	Namespace string `toml:"namespace"`
	TaskQueue string `toml:"task_queue"`
	// IdentityPrefix identifies this factory worker in Temporal.
	IdentityPrefix string `toml:"identity_prefix"`
	PayloadKeyring string `toml:"payload_keyring"`
	TLS            bool   `toml:"tls"`
	ServerName     string `toml:"server_name"`
	CAFile         string `toml:"ca_file"`
	CertFile       string `toml:"cert_file"`
	KeyFile        string `toml:"key_file"`
	APIKeyEnv      string `toml:"api_key_env"`
}

// StorageConfig controls durable artifact storage.
type StorageConfig struct {
	// DataDir is the artifact root. It must be durable and must NOT be inside a
	// sandbox.
	DataDir string `toml:"data_dir"`
}

// LimitsConfig holds per-run resource ceilings.
type LimitsConfig struct {
	AgentTimeout        tomlx.Duration `toml:"agent_timeout"`
	VerificationTimeout tomlx.Duration `toml:"verification_timeout"`
	TotalTimeout        tomlx.Duration `toml:"total_timeout"`
	SandboxIdleTimeout  tomlx.Duration `toml:"sandbox_idle_timeout"`
	CPUMilli            int            `toml:"cpu_milli"`
	MemoryMiB           int            `toml:"memory_mib"`
	DiskMiB             int            `toml:"disk_mib"`
}

// RepositoriesConfig constrains remote repository access.
type RepositoriesConfig struct {
	// Allowed is the allowlist of git URL prefixes. Empty disallows all remote
	// repositories (local sources remain available).
	Allowed []string `toml:"allowed"`
}

// ObservabilityConfig configures trace export.
type ObservabilityConfig struct {
	// OTLPEndpoint, when set, enables OTLP trace export. Empty disables it and
	// the factory uses no-op tracers, so no collector is required to run.
	OTLPEndpoint string `toml:"otel_endpoint"`
	// ServiceName identifies the factory in traces.
	ServiceName string `toml:"service_name"`
}

// HarnessConfig is the operator declaration of an agent harness.
//
// A caller names one of these keys. It can never supply an executable.
type HarnessConfig struct {
	// Type selects the harness implementation: "opencode", "unreal", or
	// "generic".
	Type string `toml:"type"`
	// Executable is the agent program (generic harness only).
	Executable string `toml:"executable"`
	// Args is the fixed argument vector (generic harness only).
	Args []string `toml:"args"`
	// ModelFlag, when set, inserts "<flag> <model>" before Args.
	ModelFlag string `toml:"model_flag"`
	// Model is the default model.
	Model string `toml:"model"`
	// Provider selects the model provider for harnesses with a native provider
	// abstraction (currently unreal).
	Provider string `toml:"provider"`
	// ThinkingLevel controls reasoning effort for unreal-agent.
	ThinkingLevel string `toml:"thinking_level"`
	// Timeout bounds one agent run.
	Timeout tomlx.Duration `toml:"timeout"`
	// Binary is the host path to a standalone agent binary pushed into the
	// sandbox.
	Binary string `toml:"binary"`
	// BinarySHA256 pins the expected content of a staged standalone binary.
	BinarySHA256 string `toml:"binary_sha256"`
	// Packages are OS packages installed in the sandbox before the agent runs.
	Packages []string `toml:"packages"`
	// PassEnv names host environment variables allowed through to the agent.
	// Everything else is dropped, so unrelated host secrets cannot leak.
	PassEnv []string `toml:"pass_env"`
	// PromptMode selects prompt delivery ("stdin_file" or "argv").
	PromptMode string `toml:"prompt_mode"`
	// BaseURL points the agent at a model gateway (opencode or unreal harness).
	BaseURL string `toml:"base_url"`
	// APIKeyEnv names the host environment variable holding the provider
	// credential (opencode or unreal harness). Its value is never persisted.
	APIKeyEnv string `toml:"api_key_env"`
	// APIKeyFile is an absolute, owner-only credential file used by
	// cube_egress mode. It is suitable for systemd credentials or Vault Agent.
	APIKeyFile string `toml:"api_key_file"`
	// CredentialMode controls where the provider credential is exposed.
	// "cube_egress" keeps it in CubeEgress and gives the agent only a harmless
	// placeholder; "environment" preserves the legacy in-process behavior.
	CredentialMode string `toml:"credential_mode"`
	// CatalogCache is a host path to the agent's model-catalog cache, staged
	// into the sandbox so model resolution does not depend on the network.
	CatalogCache string `toml:"catalog_cache"`
	// ProviderFiles maps an in-sandbox path to a host file holding the agent's
	// provider session state. Empty provisions nothing.
	//
	// SECURITY: opt-in operator configuration. The state becomes readable by the
	// agent inside the sandbox; the factory never logs it and never puts it in
	// the task prompt.
	ProviderFiles map[string]string `toml:"provider_files"`
}

// Default returns a configuration for the deployment discovered on this host.
//
// Defaults are a convenience, not an assumption: every value is overridable and
// Validate reports what is missing.
func Default() Config {
	return Config{
		FactoryVersion: Version,
		Cube:           cube.ConfigFromEnv(),
		Temporal: TemporalConfig{
			HostPort:       "127.0.0.1:7233",
			Namespace:      "default",
			TaskQueue:      "factory",
			IdentityPrefix: "factory-worker",
		},
		Storage: StorageConfig{DataDir: ".factory"},
		Sandbox: SandboxConfig{
			// Repository preparation needs git in every sandbox, whatever the
			// agent harness is. ca-certificates makes HTTPS cloning work.
			BasePackages: []string{"git", "ca-certificates"},
		},
		Limits: LimitsConfig{
			AgentTimeout:        tomlx.FromStd(30 * time.Minute),
			VerificationTimeout: tomlx.FromStd(15 * time.Minute),
			TotalTimeout:        tomlx.FromStd(90 * time.Minute),
			SandboxIdleTimeout:  tomlx.FromStd(60 * time.Minute),
			CPUMilli:            2000,
			MemoryMiB:           2048,
			DiskMiB:             1024,
		},
		Harnesses: map[string]HarnessConfig{
			agentharness.OpenCodeHarnessName: {
				Type:       "opencode",
				Binary:     envOr("FACTORY_OPENCODE_BINARY", agentharness.DefaultOpenCodeBinary),
				Model:      envOr("FACTORY_OPENCODE_MODEL", agentharness.DefaultOpenCodeModel),
				Timeout:    tomlx.FromStd(30 * time.Minute),
				Packages:   []string{"git", "ca-certificates"},
				PassEnv:    []string{"FACTORY_AGENT_TOKEN"},
				PromptMode: string(agentharness.PromptStdinFile),
				// Pre-staged model catalog: makes model resolution deterministic
				// and removes a network dependency from the critical path.
				CatalogCache: defaultCatalogCachePath(),
				// No provider session state is provisioned by default: it must
				// be an explicit operator decision.
				ProviderFiles: nil,
			},
		},
		Verification: defaultVerificationProfiles(),
		Repositories: RepositoriesConfig{},
		Models:       map[string]ModelConfig{},
		Egress:       map[string]EgressPolicyConfig{},
		Workspaces:   map[string]WorkspaceConfig{},
		Budgets: map[string]BudgetConfig{
			DefaultBudget: {},
		},
		Scopes: map[string]ScopeConfig{
			DefaultScope: {},
		},
		Review: ReviewConfig{
			Enabled: false,
			Timeout: tomlx.FromStd(DefaultReviewTimeout),
			Suspend: true,
		},
		Observability: ObservabilityConfig{
			OTLPEndpoint: envOr("FACTORY_OTEL_ENDPOINT", ""),
			ServiceName:  "factory",
		},
	}
}

// LoadConfig reads a TOML configuration file and applies environment overrides.
func LoadConfig(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read factory config %q: %w", path, err)
		}
		decoder := toml.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fmt.Errorf("parse factory config %q: %w", path, err)
		}
	}
	applyEnvOverrides(&cfg)
	return cfg, nil
}

// applyEnvOverrides lets environment variables win over the file, which is what
// makes the same config deployable to a different host.
func applyEnvOverrides(cfg *Config) {
	if v := envOr("FACTORY_DATA_DIR", ""); v != "" {
		cfg.Storage.DataDir = v
	}
	if v := envOr("TEMPORAL_HOST_PORT", ""); v != "" {
		cfg.Temporal.HostPort = v
	}
	if v := envOr("TEMPORAL_NAMESPACE", ""); v != "" {
		cfg.Temporal.Namespace = v
	}
	if v := envOr("TEMPORAL_TASK_QUEUE", ""); v != "" {
		cfg.Temporal.TaskQueue = v
	}
	if v := envOr("FACTORY_OTEL_ENDPOINT", ""); v != "" {
		cfg.Observability.OTLPEndpoint = v
	}
	// Cube settings come from the environment unless the file set them.
	if cfg.Cube.APIURL == "" {
		cfg.Cube = cube.ConfigFromEnv()
	}
}

// Validate checks the configuration at startup so a missing setting fails
// immediately rather than in the middle of a run.
func (c Config) Validate() error {
	if err := c.Cube.Validate(); err != nil {
		return err
	}
	var problems []string
	if c.Temporal.PayloadKeyring != "" {
		if _, err := loadPayloadCodec(c.Temporal.PayloadKeyring); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if _, err := c.Temporal.connectionOptions(); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.Temporal.HostPort) == "" {
		problems = append(problems, "temporal.host_port is required")
	}
	if strings.TrimSpace(c.Temporal.TaskQueue) == "" {
		problems = append(problems, "temporal.task_queue is required")
	}
	if strings.TrimSpace(c.Storage.DataDir) == "" {
		problems = append(problems, "storage.data_dir is required")
	}
	if len(c.Harnesses) == 0 {
		problems = append(problems, "at least one harness must be configured")
	}
	if err := agentharness.ValidatePackages(c.Sandbox.BasePackages); err != nil {
		problems = append(problems, "sandbox.base_packages: "+err.Error())
	}
	for name, h := range c.Harnesses {
		if strings.TrimSpace(h.Type) == "" {
			problems = append(problems, fmt.Sprintf("harness %q: type is required", name))
		}
		if h.Timeout <= 0 {
			problems = append(problems, fmt.Sprintf("harness %q: timeout must be positive", name))
		}
		mode := strings.ToLower(strings.TrimSpace(h.CredentialMode))
		if mode != "" && mode != "environment" && mode != "cube_egress" {
			problems = append(problems, fmt.Sprintf("harness %q: credential_mode must be environment or cube_egress", name))
		}
		if mode == "cube_egress" && strings.ToLower(strings.TrimSpace(h.Type)) != "unreal" {
			problems = append(problems, fmt.Sprintf("harness %q: cube_egress credential mode is currently supported only for unreal", name))
		}
		if strings.TrimSpace(h.APIKeyFile) != "" && mode != "cube_egress" {
			problems = append(problems, fmt.Sprintf("harness %q: api_key_file requires credential_mode = cube_egress", name))
		}
		if mode == "cube_egress" {
			if err := validateCubeEgressConfig(name, h); err != nil {
				problems = append(problems, err.Error())
			}
			if err := validateCredentialControlPlane(c.Cube.APIURL); err != nil {
				problems = append(problems, err.Error())
			}
		}
	}
	if len(c.Verification) == 0 {
		problems = append(problems, "at least one verification profile must be configured")
	}
	for name, profile := range c.Verification {
		if profile.Name == "" {
			profile.Name = name
		}
		if err := profile.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("verification %q: %v", name, err))
		}
	}
	for name, model := range c.Models {
		if err := model.Validate(name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for name, workspace := range c.Workspaces {
		if err := workspace.Validate(name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for name, budget := range c.Budgets {
		if err := budget.Validate(name); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for name, scope := range c.Scopes {
		if scope.Egress != "" {
			if _, ok := c.Egress[scope.Egress]; !ok {
				problems = append(problems, fmt.Sprintf("scope %q: unknown egress policy %q", name, scope.Egress))
			}
		}
		if scope.Model != "" {
			if _, ok := c.Models[scope.Model]; !ok {
				problems = append(problems, fmt.Sprintf("scope %q: unknown model %q", name, scope.Model))
			}
		}
		if scope.Workspace != "" {
			if _, ok := c.Workspaces[scope.Workspace]; !ok {
				problems = append(problems, fmt.Sprintf("scope %q: unknown workspace %q", name, scope.Workspace))
			}
		}
		if scope.Budget != "" {
			if _, ok := c.Budgets[scope.Budget]; !ok {
				problems = append(problems, fmt.Sprintf("scope %q: unknown budget %q", name, scope.Budget))
			}
		}
		for _, h := range scope.Harnesses {
			if _, ok := c.Harnesses[h]; !ok {
				problems = append(problems, fmt.Sprintf("scope %q: unknown harness %q", name, h))
			}
		}
	}
	if c.Review.Timeout < 0 {
		problems = append(problems, "review.timeout must not be negative")
	}
	for _, port := range c.Review.PreviewPorts {
		if port <= 0 || port > 65535 {
			problems = append(problems, fmt.Sprintf("review.preview_ports: %d is out of range", port))
		}
	}
	if c.Limits.AgentTimeout <= 0 {
		problems = append(problems, "limits.agent_timeout must be positive")
	}
	if c.Limits.TotalTimeout > 0 && c.Limits.AgentTimeout > c.Limits.TotalTimeout {
		problems = append(problems, "limits.agent_timeout must not exceed limits.total_timeout")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid factory configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

// VerificationProfiles returns the configured profiles as a resolved set.
func (c Config) VerificationProfiles() verification.Profiles {
	set := verification.Profiles{Profiles: map[string]verification.Profile{}}
	for name, profile := range c.Verification {
		if profile.Name == "" {
			profile.Name = name
		}
		set.Profiles[name] = profile
	}
	return set
}

// BuildHarnesses constructs the agent harness registry from configuration.
//
// This is the ONLY place a harness is created. Because the registry is keyed by
// the configured name, a request can select a harness but never define one.
func (c Config) BuildHarnesses() (*agentharness.Registry, error) {
	registry := agentharness.NewRegistry()
	// Deterministic order so error messages and logs are reproducible.
	for _, name := range sortedHarnessNames(c.Harnesses) {
		hc := c.Harnesses[name]
		var (
			harness agentharness.Harness
			err     error
		)
		switch strings.ToLower(strings.TrimSpace(hc.Type)) {
		case "opencode":
			harness, err = agentharness.NewOpenCode(agentharness.OpenCodeOptions{
				Name:          name,
				Binary:        hc.Binary,
				BinarySHA256:  hc.BinarySHA256,
				Model:         hc.Model,
				Timeout:       hc.Timeout.Std(),
				Packages:      hc.Packages,
				PassEnv:       hc.PassEnv,
				BaseURL:       hc.BaseURL,
				APIKeyEnv:     hc.APIKeyEnv,
				CatalogCache:  hc.CatalogCache,
				ProviderFiles: hc.ProviderFiles,
			})
		case "generic":
			spec := agentharness.Spec{
				Name:       name,
				Executable: hc.Executable,
				Args:       hc.Args,
				ModelFlag:  hc.ModelFlag,
				Model:      hc.Model,
				Timeout:    hc.Timeout.Std(),
				PassEnv:    hc.PassEnv,
				PromptMode: agentharness.PromptMode(hc.PromptMode),
				Provision: agentharness.Provision{
					Packages:     hc.Packages,
					BinarySource: hc.Binary,
					BinaryDest:   "/usr/local/bin/" + name,
					BinarySHA256: hc.BinarySHA256,
					VerifyArgs:   []string{name, "--version"},
				},
			}
			if hc.Binary == "" {
				spec.Provision = agentharness.Provision{Packages: hc.Packages, BinarySHA256: hc.BinarySHA256}
			}
			harness, err = agentharness.NewGeneric(spec)
		case "unreal":
			if len(hc.PassEnv) != 0 {
				return nil, fmt.Errorf("harness %q: unreal does not accept pass_env; use api_key_env for the credential", name)
			}
			if hc.Executable != "" || len(hc.Args) != 0 || hc.ModelFlag != "" || hc.PromptMode != "" || hc.CatalogCache != "" || len(hc.ProviderFiles) != 0 {
				return nil, fmt.Errorf("harness %q: unreal invocation is fixed; executable, args, model_flag, prompt_mode, catalog_cache, and provider_files are not allowed", name)
			}
			harness, err = agentharness.NewUnreal(agentharness.UnrealOptions{
				Name:          name,
				Binary:        hc.Binary,
				BinarySHA256:  hc.BinarySHA256,
				Provider:      hc.Provider,
				BaseURL:       hc.BaseURL,
				Model:         hc.Model,
				APIKeyEnv:     hc.APIKeyEnv,
				EgressManaged: strings.EqualFold(strings.TrimSpace(hc.CredentialMode), "cube_egress"),
				ThinkingLevel: hc.ThinkingLevel,
				Timeout:       hc.Timeout.Std(),
				Packages:      hc.Packages,
			})
		default:
			return nil, fmt.Errorf("harness %q: unknown type %q", name, hc.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("harness %q: %w", name, err)
		}
		if err := registry.Register(harness); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// defaultVerificationProfiles provides a small, safe default so the factory can
// run before an operator writes their own profile.
//
// The default deliberately runs the repository's own scripts when present and
// does not invent a build system.
func defaultVerificationProfiles() map[string]verification.Profile {
	mandatory := true
	return map[string]verification.Profile{
		"default": {
			Name: "default",
			Steps: []verification.Step{
				{
					ID:          "build",
					Argv:        []string{"./build.sh"},
					Mandatory:   &mandatory,
					Description: "repository build script",
					Timeout:     tomlx.FromStd(10 * time.Minute),
				},
				{
					ID:          "unit-tests",
					Argv:        []string{"./test.sh"},
					Mandatory:   &mandatory,
					Description: "repository test script",
					Timeout:     tomlx.FromStd(10 * time.Minute),
				},
			},
		},
	}
}

// defaultCatalogCachePath returns the opencode catalog cache for this user, if
// one exists. It is a convenience default, not a requirement.
func defaultCatalogCachePath() string {
	if v := strings.TrimSpace(os.Getenv("FACTORY_OPENCODE_CATALOG_CACHE")); v != "" {
		return v
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(cacheDir, "opencode", "models.json")
	if _, err := os.Stat(candidate); err != nil {
		return ""
	}
	return candidate
}

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func sortedHarnessNames(m map[string]HarnessConfig) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	// Small map; insertion sort keeps the dependency list minimal and the order
	// deterministic.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}
