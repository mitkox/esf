package agentharness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mitkox/esf/internal/sandbox"
)

const (
	DefaultUnrealTimeout      = 30 * time.Minute
	UnrealBinaryPath          = "/usr/local/bin/unreal-agent-runner"
	UnrealSessionDirectory    = "/workspace/.factory/unreal/sessions"
	UnrealLogDirectory        = "/workspace/.factory/unreal/logs"
	unrealAPIKeyEnvironment   = "UNREAL_HARNESS_LLM_API_KEY"
	unrealBaseURLEnvironment  = "UNREAL_HARNESS_LLM_BASE_URL"
	unrealModelEnvironment    = "UNREAL_HARNESS_LLM_MODEL"
	unrealProviderEnvironment = "UNREAL_HARNESS_LLM_PROVIDER"
	unrealEgressPlaceholder   = "cube-egress-managed-placeholder"
)

var supportedUnrealProviders = map[string]bool{
	"fireworks":  true,
	"ollama":     true,
	"openai":     true,
	"openrouter": true,
}

var unrealModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\-]{0,255}$`)

// UnrealOptions configures the standalone unreal-agent runner. ESF depends
// only on its command-line/JSON contract, never on its Go module.
type UnrealOptions struct {
	Name          string
	Binary        string
	BinarySHA256  string
	Provider      string
	BaseURL       string
	Model         string
	APIKeyEnv     string
	EgressManaged bool
	ThinkingLevel string
	Timeout       time.Duration
	Packages      []string
	ReadFile      func(string) ([]byte, error)
	LookupEnv     func(string) (string, bool)
}

type unrealRequest struct {
	Messages      []unrealMessage `json:"messages"`
	Model         string          `json:"model"`
	SessionID     string          `json:"session_id"`
	ThinkingLevel string          `json:"thinking_level"`
}

type unrealMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	MessageID string `json:"message_id"`
}

// NewUnreal constructs a production-safe unreal-agent harness.
func NewUnreal(opts UnrealOptions) (*GenericCommandHarness, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "unreal"
	}
	binary := strings.TrimSpace(opts.Binary)
	if binary == "" {
		return nil, fmt.Errorf("agentharness: unreal binary is required")
	}
	binarySHA256 := strings.TrimSpace(opts.BinarySHA256)
	if binarySHA256 == "" {
		return nil, fmt.Errorf("agentharness: unreal binary_sha256 is required")
	}
	provider := strings.ToLower(strings.TrimSpace(opts.Provider))
	if !supportedUnrealProviders[provider] {
		return nil, fmt.Errorf("agentharness: unreal provider %q is unsupported", opts.Provider)
	}
	model, err := validateUnrealModel(opts.Model)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimSpace(opts.BaseURL)
	var parsedBaseURL *url.URL
	if baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return nil, fmt.Errorf("agentharness: unreal base_url must be an http(s) URL without userinfo")
		}
		parsedBaseURL = parsed
	}
	if opts.EgressManaged {
		if provider == "ollama" {
			return nil, fmt.Errorf("agentharness: unreal cube egress credentials are not valid for ollama")
		}
		if parsedBaseURL == nil || parsedBaseURL.Scheme != "https" || parsedBaseURL.RawQuery != "" || parsedBaseURL.Fragment != "" {
			return nil, fmt.Errorf("agentharness: unreal cube egress credentials require an https base_url without query or fragment")
		}
	}
	apiKeyEnv := strings.TrimSpace(opts.APIKeyEnv)
	if provider != "ollama" && apiKeyEnv == "" && !opts.EgressManaged {
		return nil, fmt.Errorf("agentharness: unreal api_key_env is required for provider %q", provider)
	}
	if apiKeyEnv != "" {
		if err := ValidateEnvironmentName(apiKeyEnv); err != nil {
			return nil, fmt.Errorf("agentharness: unreal api_key_env: %w", err)
		}
	}
	thinkingLevel := strings.TrimSpace(opts.ThinkingLevel)
	if thinkingLevel == "" {
		thinkingLevel = "high"
	}
	switch thinkingLevel {
	case "low", "medium", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("agentharness: unreal thinking_level must be one of low, medium, high, xhigh, max")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultUnrealTimeout
	}

	generic, err := NewGeneric(Spec{
		Name:       name,
		Executable: UnrealBinaryPath,
		Args: []string{
			"-workspace", ".",
			"-session-directory", UnrealSessionDirectory,
			"-log-directory", UnrealLogDirectory,
		},
		Model:      model,
		Timeout:    timeout,
		PromptMode: PromptStdinFile,
		Provision: Provision{
			Packages:     opts.Packages,
			BinarySource: binary,
			BinaryDest:   UnrealBinaryPath,
			BinarySHA256: binarySHA256,
			// -h is part of the runner's stable flag contract. The executable
			// hash, recorded separately by Version, identifies the exact binary.
			VerifyArgs: []string{UnrealBinaryPath, "-h"},
		},
	})
	if err != nil {
		return nil, err
	}
	if opts.ReadFile != nil {
		generic.readFile = opts.ReadFile
	}
	lookupEnv := opts.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	generic.validateModel = func(model string) error {
		model = strings.TrimSpace(model)
		if model == "" || model == generic.Model() {
			return nil
		}
		return fmt.Errorf("%s: model override %q is not allowed; configured model is %q", generic.Name(), model, generic.Model())
	}
	generic.transformTask = func(task Task) (Task, error) {
		return transformUnrealTask(generic, task, provider, baseURL, apiKeyEnv, thinkingLevel, opts.EgressManaged, lookupEnv)
	}
	generic.version = unrealVersion
	return generic, nil
}

// transformUnrealTask writes a versioned JSON request to stdin. Stable session and message IDs
// make a resumed invocation deduplicate the task instead of applying it twice.
func transformUnrealTask(
	h *GenericCommandHarness,
	task Task,
	provider, baseURL, apiKeyEnv, thinkingLevel string,
	egressManaged bool,
	lookupEnv func(string) (string, bool),
) (Task, error) {
	if strings.TrimSpace(task.RunID) == "" {
		return Task{}, fmt.Errorf("%s: run ID is required", h.Name())
	}
	model := strings.TrimSpace(task.Model)
	if model == "" {
		model = h.Model()
	}
	if err := h.ValidateModel(model); err != nil {
		return Task{}, err
	}
	model, err := validateUnrealModel(model)
	if err != nil {
		return Task{}, err
	}
	request := unrealRequest{
		Messages: []unrealMessage{{
			Role: "user", Content: task.Prompt,
			MessageID: stableUnrealMessageID(task.RunID, task.Prompt),
		}},
		Model: model, SessionID: stableUnrealID("session", task.RunID),
		ThinkingLevel: thinkingLevel,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Task{}, fmt.Errorf("%s: encode request: %w", h.Name(), err)
	}

	env := make(map[string]string, len(task.Env)+4)
	for name, value := range task.Env {
		env[name] = value
	}
	// These names are owned by the harness. Per-run activity input cannot
	// redirect the provider or replace the operator-approved credential.
	for _, name := range []string{unrealAPIKeyEnvironment, unrealBaseURLEnvironment, unrealModelEnvironment, unrealProviderEnvironment} {
		delete(env, name)
	}
	env[unrealProviderEnvironment] = provider
	if baseURL != "" {
		env[unrealBaseURLEnvironment] = baseURL
	}
	if egressManaged {
		env[unrealAPIKeyEnvironment] = unrealEgressPlaceholder
	} else if apiKeyEnv != "" {
		value, ok := lookupEnv(apiKeyEnv)
		if !ok || strings.TrimSpace(value) == "" {
			return Task{}, fmt.Errorf("%s: credential environment variable %s is empty", h.Name(), apiKeyEnv)
		}
		env[unrealAPIKeyEnvironment] = value
	}

	task.Prompt = string(encoded)
	task.Model = model
	task.Env = env
	return task, nil
}

func validateUnrealModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("agentharness: unreal model is required")
	}
	if !unrealModelPattern.MatchString(model) {
		return "", fmt.Errorf("agentharness: unreal model %q contains unsupported characters or exceeds 256 bytes", model)
	}
	return model, nil
}

// Version records a content digest rather than trusting optional CLI version
// output. This also works with development builds of the standalone runner.
func unrealVersion(ctx context.Context, sb sandbox.Sandbox) string {
	exec, err := sb.Execute(ctx, sandbox.Command{
		Argv: []string{"sha256sum", UnrealBinaryPath}, Timeout: time.Minute,
		Description: "read unreal-agent runner digest",
	})
	if err != nil || !exec.Succeeded() {
		return ""
	}
	fields := strings.Fields(exec.Stdout)
	if len(fields) == 0 || len(fields[0]) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return ""
	}
	return "sha256:" + strings.ToLower(fields[0])
}

func stableUnrealID(kind string, values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "factory-" + kind + "-" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func stableUnrealMessageID(runID, prompt string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(stableUnrealID("message", runID, prompt))).String()
}
