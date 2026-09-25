package agentharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// OpenCode harness defaults.
//
// These were established by inspecting the locally installed CLI
// (`opencode v2.0.9`), not guessed:
//
//	opencode2 run --help
//	  ARGUMENTS  message... string    Message to send (optional)
//	  FLAGS      --standalone         Run with a private server
//	             --auto               Auto-approve permissions
//	             --format choice      default|json
//	             --model, -m string   provider/model#variant
//
// The prompt is delivered on standard input: with no message argument the CLI
// reads the message from stdin (verified). That lets the factory pass untrusted
// task text without any shell interpolation.
const (
	// DefaultOpenCodeBinary is the pinned standalone binary on this host.
	DefaultOpenCodeBinary = "/home/USER/.npm-global/lib/node_modules/@opencode/cli/bin/opencode.exe"
	// OpenCodeConfigPath is where the harness writes the agent configuration.
	OpenCodeConfigPath = "/root/.config/opencode/opencode.json"
	// DefaultOpenCodeTimeout bounds a single agent run.
	DefaultOpenCodeTimeout = 30 * time.Minute
	// DefaultOpenCodeModel is reachable from inside a Cube microVM over the
	// sandbox's public egress. Host-local model endpoints are NOT reachable.
	DefaultOpenCodeModel = "opencode-go/deepseek-v4.1-flash"
)

// OpenCodeOptions configures the OpenCode harness.
type OpenCodeOptions struct {
	// Name is the registry name. Defaults to "opencode2".
	Name string
	// Binary is the host path to the standalone opencode binary.
	Binary string
	// BinarySHA256 optionally pins the staged binary content.
	BinarySHA256 string
	// Model is the default model (provider/model). Must be reachable from
	// inside the sandbox.
	Model string
	// Timeout bounds one agent run.
	Timeout time.Duration
	// Packages are OS packages installed in the sandbox before the agent runs.
	Packages []string
	// PassEnv names host environment variables allowed through to the agent,
	// e.g. a provider credential. Nothing else is passed.
	PassEnv []string
	// Config overrides the generated agent configuration. When empty a
	// conservative default is generated.
	Config map[string]any
	// BaseURL, when set, points the agent at a model gateway. It is written into
	// the generated configuration.
	BaseURL string
	// APIKeyEnv names the environment variable the agent reads its credential
	// from. The value is never written into the sandbox configuration file.
	APIKeyEnv string
	// CatalogCache is a host path to the agent's model-catalog cache. When set
	// it is staged into the sandbox so the agent does not have to fetch the
	// catalog from the network on every run.
	CatalogCache string
	// CatalogCachePath is the in-sandbox destination for CatalogCache.
	CatalogCachePath string
	// ProviderFiles maps an absolute IN-SANDBOX path to a file on the factory
	// host that holds the agent's provider session state.
	//
	// SECURITY: this deliberately makes operator-provisioned provider state
	// readable by the agent inside the sandbox. It is opt-in operator
	// configuration, never task-controlled, and never written into the task
	// prompt. The MVP accepts this trade-off because the alternative — an
	// egress proxy that injects the header — is a Phase 2 concern. Leaving it
	// empty provisions nothing.
	ProviderFiles map[string]string
	// ReadFile, for tests, overrides how the agent binary is read.
	ReadFile func(string) ([]byte, error)
}

// NewOpenCode builds the OpenCode harness as a configured GenericCommandHarness.
//
// Nothing here is reachable from an API client: the executable, arguments,
// permissions and timeouts are all operator configuration.
func NewOpenCode(opts OpenCodeOptions) (*GenericCommandHarness, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "opencode2"
	}
	binary := strings.TrimSpace(opts.Binary)
	if binary == "" {
		binary = DefaultOpenCodeBinary
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = DefaultOpenCodeModel
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultOpenCodeTimeout
	}
	packages := opts.Packages
	if len(packages) == 0 {
		// The base template ships neither git nor a JavaScript runtime; git is
		// required for baseline, diff and patch extraction.
		packages = []string{"git", "ca-certificates"}
	}

	cfg := opts.Config
	if cfg == nil {
		var err error
		cfg, err = defaultOpenCodeConfig(opts.BaseURL, opts.APIKeyEnv, model)
		if err != nil {
			return nil, err
		}
	}
	configBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("agentharness: encode opencode config: %w", err)
	}

	h, err := NewGeneric(Spec{
		Name:       name,
		Executable: "opencode2",
		// --standalone keeps the agent from depending on a host-side background
		// service; --auto approves permissions non-interactively; --format json
		// gives machine-readable output the factory can record.
		// The model flag must come AFTER the "run" subcommand: opencode2 parses
		// subcommands first, so a leading --model is rejected.
		Args:       []string{"run", ModelArgsPlaceholder, "--standalone", "--auto", "--format", "json"},
		ModelFlag:  "--model",
		Model:      model,
		Timeout:    timeout,
		PromptMode: PromptStdinFile,
		PassEnv:    opts.PassEnv,
		Provision: Provision{
			Packages:     packages,
			BinarySource: binary,
			BinaryDest:   "/usr/local/bin/opencode2",
			BinarySHA256: opts.BinarySHA256,
			Files:        map[string]string{OpenCodeConfigPath: string(configBytes)},
			SeedFiles:    mergeSeedFiles(seedFiles(opts.CatalogCache, opts.CatalogCachePath), opts.ProviderFiles),
			VerifyArgs:   []string{"opencode2", "--version"},
		},
	})
	if err != nil {
		return nil, err
	}
	if opts.ReadFile != nil {
		h.readFile = opts.ReadFile
	}
	h.configureModelEndpoint = func(ctx context.Context, sb sandbox.Sandbox, endpoint ModelEndpoint) error {
		if endpoint.BaseURL == "" {
			return nil
		}
		cfg, err := defaultOpenCodeConfig(endpoint.BaseURL, endpoint.APIKeyEnv, endpoint.Model)
		if err != nil {
			return err
		}
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("agentharness: encode resolved opencode model config: %w", err)
		}
		if err := sb.WriteFile(ctx, OpenCodeConfigPath, data); err != nil {
			return fmt.Errorf("agentharness: write resolved opencode model config: %w", err)
		}
		return nil
	}
	return h, nil
}

// OpenCodeCatalogCachePath is where opencode caches the models.dev catalog.
const OpenCodeCatalogCachePath = "/root/.cache/opencode/models.json"

// OpenCodeAuthPath is where opencode stores provider session state.
const OpenCodeAuthPath = "/root/.local/share/opencode/auth.json"

// mergeSeedFiles combines seed-file maps, returning nil when both are empty.
func mergeSeedFiles(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	merged := map[string]string{}
	for dest, src := range a {
		merged[dest] = src
	}
	for dest, src := range b {
		merged[dest] = src
	}
	return merged
}

// seedFiles returns the seed-file map for the harness, or nil when no catalog
// cache is configured.
func seedFiles(cachePath, dest string) map[string]string {
	if strings.TrimSpace(cachePath) == "" {
		return nil
	}
	if strings.TrimSpace(dest) == "" {
		dest = OpenCodeCatalogCachePath
	}
	return map[string]string{dest: cachePath}
}

// defaultOpenCodeConfig builds the agent configuration written into the sandbox.
//
// It is deliberately explicit rather than empty so that a run's agent behaviour
// is reproducible and does not depend on whatever user configuration happens to
// exist on the host.
func defaultOpenCodeConfig(baseURL, apiKeyEnv, model string) (map[string]any, error) {
	cfg := map[string]any{
		"$schema":       "https://opencode.ai/config.json",
		"default_agent": "build",
		"shell":         "/bin/bash",
	}
	if baseURL != "" {
		provider := map[string]any{
			"name":    "factory",
			"package": "@opencode/ai/providers/openai-compatible",
			"settings": map[string]any{
				"baseURL": baseURL,
			},
		}
		if apiKeyEnv != "" {
			// The value is supplied through the sandbox environment, never
			// persisted into this file.
			provider["settings"].(map[string]any)["apiKey"] = "{env:" + apiKeyEnv + "}"
		}
		if model = strings.TrimSpace(model); model != "" {
			modelID := model
			if index := strings.IndexByte(model, '/'); index >= 0 {
				modelID = model[index+1:]
			}
			provider["models"] = map[string]any{
				modelID: map[string]any{
					"name":    modelID,
					"modelID": modelID,
				},
			}
			cfg["model"] = "factory/" + modelID
		}
		cfg["providers"] = map[string]any{"factory": provider}
	}
	return cfg, nil
}

// OpenCodeHarnessName is the default registry name.
const OpenCodeHarnessName = "opencode2"
