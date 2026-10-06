package agentharness

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

const PiContractVersion = "esf/pi/v1"
const PiRuntimePath = "/opt/esf/agents/node"
const piKeyEnvironment = "ESF_PI_API_KEY"
const piSummaryLimit = 128 << 10

// piPinnedCatalog is generated only from pi-ai 1.0.4 in the committed lockfile.
//
//go:embed pi-catalog.json
var piPinnedCatalog []byte

// PiOptions pins the complete execution chain. v1 only runs a preinstalled,
// bundled runner and Node runtime; no dependency resolution happens in a run.
type PiOptions struct {
	Name, Binary, BinarySHA256, RuntimeBinary, RuntimeSHA256              string
	Preinstalled                                                          bool
	Provider, BaseURL, Model, API, APIKeyEnv, ThinkingLevel, CatalogCache string
	EgressManaged                                                         bool
	Timeout                                                               time.Duration
	MaxProcessRestarts                                                    *int
	ReadFile                                                              func(string) ([]byte, error)
	LookupEnv                                                             func(string) (string, bool)
}

// PiModelMetadata is an offline model definition, with explicit limits and
// pricing provenance. Model prices are estimates, never provider billing.
type PiModelMetadata struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	API              string             `json:"api"`
	Reasoning        bool               `json:"reasoning"`
	Input            []string           `json:"input"`
	ContextWindow    int                `json:"contextWindow"`
	MaxTokens        int                `json:"maxTokens"`
	Cost             *PiModelCost       `json:"cost,omitempty"`
	PricingKnown     bool               `json:"pricingKnown"`
	ThinkingLevels   []string           `json:"thinkingLevels"`
	ThinkingLevelMap map[string]*string `json:"thinkingLevelMap,omitempty"`
	Compat           json.RawMessage    `json:"compat,omitempty"`
}
type PiModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

func (c *PiModelCost) UnmarshalJSON(data []byte) error {
	var rates map[string]*float64
	if err := json.Unmarshal(data, &rates); err != nil {
		return err
	}
	if len(rates) != 4 || rates["input"] == nil || rates["output"] == nil || rates["cacheRead"] == nil || rates["cacheWrite"] == nil {
		return fmt.Errorf("cost must explicitly declare input, output, cacheRead, and cacheWrite rates")
	}
	c.Input, c.Output, c.CacheRead, c.CacheWrite = *rates["input"], *rates["output"], *rates["cacheRead"], *rates["cacheWrite"]
	return nil
}

type piRequest struct {
	ContractVersion    string          `json:"contract_version"`
	RunID              string          `json:"run_id"`
	RequestID          string          `json:"request_id"`
	Prompt             string          `json:"prompt"`
	RepositoryDir      string          `json:"repository_dir"`
	StateDir           string          `json:"state_dir"`
	Provider           string          `json:"provider"`
	BaseURL            string          `json:"base_url"`
	Model              string          `json:"model"`
	API                string          `json:"api"`
	ThinkingLevel      string          `json:"thinking_level"`
	TimeoutMS          int64           `json:"timeout_ms"`
	MaxProcessRestarts int             `json:"max_process_restarts"`
	RunnerSHA256       string          `json:"runner_sha256"`
	RuntimeSHA256      string          `json:"runtime_sha256"`
	Metadata           PiModelMetadata `json:"metadata"`
	Fingerprint        string          `json:"fingerprint,omitempty"`
}
type piTerminal struct {
	ContractVersion string `json:"contract_version"`
	RunID           string `json:"run_id"`
	RequestID       string `json:"request_id"`
	Fingerprint     string `json:"fingerprint"`
	Model           string `json:"model"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	Restarts        int    `json:"restarts"`
}
type piUsage struct {
	ContractVersion string                     `json:"contract_version"`
	RunID           string                     `json:"run_id"`
	Fingerprint     string                     `json:"fingerprint"`
	Complete        bool                       `json:"complete"`
	TokensIn        *int64                     `json:"tokens_in"`
	TokensOut       *int64                     `json:"tokens_out"`
	CostUSD         *float64                   `json:"cost_usd"`
	CostBasis       string                     `json:"cost_basis"`
	Models          map[string]piModelUsage    `json:"models"`
	Tools           map[string]json.RawMessage `json:"tools"`
}

type piModelUsage struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cacheRead"`
	CacheWrite  int64 `json:"cacheWrite"`
	TotalTokens int64 `json:"totalTokens"`
}

type PiHarness struct {
	generic  *GenericCommandHarness
	opts     PiOptions
	metadata PiModelMetadata
	restarts int
}

func NewPi(opts PiOptions) (*PiHarness, error) {
	if opts.Name == "" {
		opts.Name = "pi"
	}
	if !opts.Preinstalled {
		return nil, fmt.Errorf("pi: v1 requires preinstalled = true")
	}
	if !path.IsAbs(opts.Binary) || !validPiDigest(opts.BinarySHA256) {
		return nil, fmt.Errorf("pi: absolute binary and binary_sha256 are required")
	}
	if opts.RuntimeBinary == "" {
		opts.RuntimeBinary = PiRuntimePath
	}
	if !path.IsAbs(opts.RuntimeBinary) || !validPiDigest(opts.RuntimeSHA256) {
		return nil, fmt.Errorf("pi: absolute runtime_binary and runtime_sha256 are required")
	}
	if opts.API == "" {
		opts.API = "openai-completions"
	}
	if opts.API != "openai-completions" && opts.API != "openai-responses" {
		return nil, fmt.Errorf("pi: unsupported api %q", opts.API)
	}
	if !harnessNamePattern.MatchString(opts.Provider) {
		return nil, fmt.Errorf("pi: provider is required and must be a simple identifier")
	}
	if _, err := validateUnrealModel(opts.Model); err != nil {
		return nil, fmt.Errorf("pi: %w", err)
	}
	endpoint, err := url.Parse(opts.BaseURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("pi: base_url must be an http(s) URL without credentials, query, or fragment")
	}
	if opts.EgressManaged && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("pi: cube_egress requires https")
	}
	if opts.APIKeyEnv != "" {
		if err := ValidateEnvironmentName(opts.APIKeyEnv); err != nil {
			return nil, err
		}
	}
	if !opts.EgressManaged && opts.Provider != "ollama" && opts.Provider != "local" && opts.APIKeyEnv == "" {
		return nil, fmt.Errorf("pi: provider credential source is required")
	}
	if opts.ReadFile == nil {
		opts.ReadFile = func(name string) ([]byte, error) {
			stat, err := os.Stat(name)
			if err != nil {
				return nil, err
			}
			if !stat.Mode().IsRegular() || stat.Size() > piSummaryLimit {
				return nil, fmt.Errorf("model definition must be a bounded regular file")
			}
			file, err := os.Open(name)
			if err != nil {
				return nil, err
			}
			defer file.Close()
			stat, err = file.Stat()
			if err != nil {
				return nil, err
			}
			if !stat.Mode().IsRegular() || stat.Size() > piSummaryLimit {
				return nil, fmt.Errorf("model definition must be a bounded regular file")
			}
			return io.ReadAll(io.LimitReader(file, piSummaryLimit+1))
		}
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	var data []byte
	if opts.CatalogCache != "" {
		data, err = opts.ReadFile(opts.CatalogCache)
	} else {
		var catalog map[string]map[string]json.RawMessage
		if err = json.Unmarshal(piPinnedCatalog, &catalog); err == nil {
			data = catalog[opts.Provider][opts.Model]
		}
		if len(data) == 0 && err == nil {
			err = fmt.Errorf("model absent from pinned catalog; catalog_cache must provide explicit metadata")
		}
	}
	if err != nil {
		return nil, fmt.Errorf("pi: read model definition: %w", err)
	}
	if len(data) > piSummaryLimit {
		return nil, fmt.Errorf("pi: model definition exceeds limit")
	}
	var metadata PiModelMetadata
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		return nil, fmt.Errorf("pi: decode model definition: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("pi: model definition must contain exactly one JSON object")
	}
	if metadata.ID != opts.Model || metadata.API != opts.API || metadata.ContextWindow <= 0 || metadata.ContextWindow > (1<<53)-1 || metadata.MaxTokens <= 0 || metadata.MaxTokens >= metadata.ContextWindow {
		return nil, fmt.Errorf("pi: model definition must match model/api and declare positive context/output limits")
	}
	if metadata.Name == "" {
		metadata.Name = metadata.ID
	}
	if len(metadata.Compat) != 0 {
		var compat map[string]json.RawMessage
		if err := json.Unmarshal(metadata.Compat, &compat); err != nil || compat == nil {
			return nil, fmt.Errorf("pi: compat must be an API compatibility object")
		}
	}
	if len(metadata.Input) != 1 || metadata.Input[0] != "text" {
		return nil, fmt.Errorf("pi: v1 model definition requires input = [text]")
	}
	if metadata.PricingKnown && metadata.Cost == nil {
		return nil, fmt.Errorf("pi: known pricing requires cost rates")
	}
	if metadata.Cost != nil {
		for _, v := range []float64{metadata.Cost.Input, metadata.Cost.Output, metadata.Cost.CacheRead, metadata.Cost.CacheWrite} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return nil, fmt.Errorf("pi: invalid cost rate")
			}
		}
	}
	if opts.ThinkingLevel == "" {
		opts.ThinkingLevel = "off"
	}
	validThinking := false
	for _, v := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if opts.ThinkingLevel == v {
			validThinking = true
		}
	}
	if !validThinking {
		return nil, fmt.Errorf("pi: unsupported thinking_level")
	}
	if mapped, exists := metadata.ThinkingLevelMap[opts.ThinkingLevel]; exists && (mapped == nil || *mapped == "") {
		return nil, fmt.Errorf("pi: selected thinking_level is disabled by the model mapping")
	}
	if opts.ThinkingLevel == "xhigh" || opts.ThinkingLevel == "max" {
		if metadata.ThinkingLevelMap[opts.ThinkingLevel] == nil {
			return nil, fmt.Errorf("pi: extended thinking_level requires an explicit provider mapping")
		}
	}
	if opts.ThinkingLevel != "off" {
		supported := false
		for _, v := range metadata.ThinkingLevels {
			if v == opts.ThinkingLevel {
				supported = true
			}
		}
		if !metadata.Reasoning || !supported {
			return nil, fmt.Errorf("pi: thinking_level is not supported by the pinned model definition")
		}
	}
	restarts := 1
	if opts.MaxProcessRestarts != nil {
		restarts = *opts.MaxProcessRestarts
	}
	if restarts < 0 || restarts > 1 {
		return nil, fmt.Errorf("pi: max_process_restarts must be 0 or 1")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Minute
	}
	if opts.Timeout.Milliseconds() > 2147483647 {
		return nil, fmt.Errorf("pi: timeout exceeds supervisor timer limit")
	}
	generic, err := NewGeneric(Spec{Name: opts.Name, Executable: opts.RuntimeBinary, Args: []string{"--use-system-ca", opts.Binary}, Model: opts.Model, Timeout: opts.Timeout, PromptMode: PromptStdinFile, Provision: Provision{Preinstalled: true, BinaryDest: opts.Binary, BinarySHA256: opts.BinarySHA256}})
	if err != nil {
		return nil, err
	}
	return &PiHarness{generic: generic, opts: opts, metadata: metadata, restarts: restarts}, nil
}

func validPiDigest(d string) bool {
	b, e := hex.DecodeString(d)
	return e == nil && len(b) == sha256.Size
}
func (h *PiHarness) Name() string  { return h.generic.Name() }
func (h *PiHarness) Model() string { return h.opts.Model }
func (h *PiHarness) ValidateModel(m string) error {
	if m != "" && m != h.Model() {
		return fmt.Errorf("pi: model override %q differs from configured model", m)
	}
	return nil
}
func (h *PiHarness) ValidateModelEndpoint(e ModelEndpoint) error {
	for _, pair := range [][2]string{{e.Provider, h.opts.Provider}, {e.Model, h.opts.Model}, {e.BaseURL, h.opts.BaseURL}, {e.APIKeyEnv, h.opts.APIKeyEnv}} {
		if pair[0] != "" && pair[0] != pair[1] {
			return fmt.Errorf("pi: resolved model endpoint differs from configured provider, model, base URL, or credential source")
		}
	}
	return nil
}
func (h *PiHarness) ConfigureModelEndpoint(_ context.Context, _ sandbox.Sandbox, e ModelEndpoint) error {
	return h.ValidateModelEndpoint(e)
}
func (h *PiHarness) Provision(ctx context.Context, sb sandbox.Sandbox) error {
	// Check the interpreter before executing any runner-controlled code.
	exec, err := sb.Execute(ctx, sandbox.Command{Argv: []string{"sha256sum", h.opts.RuntimeBinary}, Timeout: time.Minute, Description: "verify Pi Node runtime digest"})
	if err != nil {
		return err
	}
	fields := strings.Fields(exec.Stdout)
	if !exec.Succeeded() || len(fields) == 0 || !strings.EqualFold(fields[0], h.opts.RuntimeSHA256) {
		return fmt.Errorf("pi: runtime SHA-256 mismatch")
	}
	if err := h.generic.Provision(ctx, sb); err != nil {
		return err
	}
	exec, err = sb.Execute(ctx, sandbox.Command{Argv: []string{h.opts.RuntimeBinary, h.opts.Binary, "--version"}, Timeout: time.Minute, Description: "verify Pi runner and runtime"})
	if err != nil {
		return err
	}
	if !exec.Succeeded() || !strings.HasPrefix(strings.TrimSpace(exec.Stdout), "esf-pi/1 pi-durable/1.0.4 node/") {
		return fmt.Errorf("pi: runner self-check failed")
	}
	return nil
}
func (h *PiHarness) Version(context.Context, sandbox.Sandbox) string {
	return "pi-durable:1.0.4;runner-sha256:" + strings.ToLower(h.opts.BinarySHA256) + ";node-sha256:" + strings.ToLower(h.opts.RuntimeSHA256)
}
func (h *PiHarness) request(task Task) (piRequest, error) {
	if task.RunID == "" || strings.TrimSpace(task.Prompt) == "" || !path.IsAbs(task.RepositoryDir) {
		return piRequest{}, fmt.Errorf("pi: run ID, task, and absolute repository directory are required")
	}
	if err := h.ValidateModel(task.Model); err != nil {
		return piRequest{}, err
	}
	timeout := task.Timeout
	if timeout <= 0 {
		timeout = h.opts.Timeout
	}
	if timeout.Milliseconds() <= 0 || timeout.Milliseconds() > 2147483647 {
		return piRequest{}, fmt.Errorf("pi: timeout outside supervisor timer limits")
	}
	id := sha256.Sum256([]byte(task.RunID))
	requestID := sha256.Sum256([]byte(task.RunID + "\x00" + task.Prompt))
	req := piRequest{ContractVersion: PiContractVersion, RunID: task.RunID, RequestID: hex.EncodeToString(requestID[:]), Prompt: task.Prompt, RepositoryDir: path.Clean(task.RepositoryDir), StateDir: path.Join(path.Dir(path.Clean(task.RepositoryDir)), ".factory", "pi", hex.EncodeToString(id[:])), Provider: h.opts.Provider, BaseURL: h.opts.BaseURL, Model: h.opts.Model, API: h.opts.API, ThinkingLevel: h.opts.ThinkingLevel, TimeoutMS: timeout.Milliseconds(), MaxProcessRestarts: h.restarts, RunnerSHA256: strings.ToLower(h.opts.BinarySHA256), RuntimeSHA256: strings.ToLower(h.opts.RuntimeSHA256), Metadata: h.metadata}
	b, _ := json.Marshal(req)
	digest := sha256.Sum256(b)
	req.Fingerprint = hex.EncodeToString(digest[:])
	return req, nil
}
func (h *PiHarness) Run(ctx context.Context, sb sandbox.Sandbox, task Task) (Result, error) {
	req, err := h.request(task)
	if err != nil {
		return Result{}, err
	}
	// The canonical credential is owned by this adapter. Unrelated task/host
	// environment, including settings that redirect providers, never reaches Pi.
	key := "cube-egress-managed-placeholder"
	if !h.opts.EgressManaged && h.opts.APIKeyEnv != "" {
		var ok bool
		key, ok = h.opts.LookupEnv(h.opts.APIKeyEnv)
		if !ok || strings.TrimSpace(key) == "" {
			return Result{}, fmt.Errorf("pi: credential environment variable %s is empty", h.opts.APIKeyEnv)
		}
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return Result{}, err
	}
	task.Prompt = string(encoded)
	task.Env = map[string]string{piKeyEnvironment: key}
	// The supervisor owns the task deadline. Allow bounded time to persist
	// its timeout/cancellation receipts before the data plane kills it.
	task.Timeout = time.Duration(req.TimeoutMS)*time.Millisecond + 5*time.Second
	result, err := h.generic.Run(ctx, sb, task)
	if err != nil {
		return result, err
	}
	result.Version = h.Version(ctx, sb)
	result.Evidence = map[string][]byte{}
	var terminal piTerminal
	terminalBytes, readErr := sb.ReadFile(ctx, path.Join(req.StateDir, "terminal.json"))
	if readErr == nil && len(terminalBytes) <= piSummaryLimit {
		readErr = json.Unmarshal(terminalBytes, &terminal)
	} else if readErr == nil {
		readErr = fmt.Errorf("terminal summary exceeds limit")
	}
	if readErr != nil || terminal.ContractVersion != PiContractVersion || terminal.RunID != req.RunID || terminal.RequestID != req.RequestID || terminal.Fingerprint != req.Fingerprint || terminal.Model != req.Model || terminal.Restarts < 0 || terminal.Restarts > h.restarts || (terminal.Status != "completed" && terminal.Status != "failed" && terminal.Status != "cancelled" && terminal.Status != "timeout") {
		result.ExitCode = 1
		result.Stderr += "\npi: missing, malformed, or mismatched terminal summary"
		return result, nil
	}
	result.Evidence["agent/pi-terminal.json"] = terminalBytes
	if terminal.Status != "completed" {
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
		result.Stderr += "\npi: " + terminal.Error
	}
	if terminal.Status == "timeout" {
		result.TimedOut = true
	}
	usageBytes, e := sb.ReadFile(ctx, path.Join(req.StateDir, "usage.json"))
	var usage piUsage
	if e == nil && len(usageBytes) <= piSummaryLimit && json.Unmarshal(usageBytes, &usage) == nil && usage.ContractVersion == PiContractVersion && usage.RunID == req.RunID && usage.Fingerprint == req.Fingerprint {
		valid := true
		if usage.CostBasis != "estimated" && usage.CostBasis != "unavailable" {
			valid = false
		}
		for _, v := range []*int64{usage.TokensIn, usage.TokensOut} {
			if v != nil && *v < 0 {
				valid = false
			}
		}
		if usage.CostUSD != nil && (!h.metadata.PricingKnown || usage.CostBasis != "estimated" || math.IsNaN(*usage.CostUSD) || math.IsInf(*usage.CostUSD, 0) || *usage.CostUSD < 0) {
			valid = false
		}
		if usage.Complete {
			u, ok := usage.Models[req.Provider+"/"+req.Model]
			if !ok || len(usage.Models) != 1 || usage.TokensIn == nil || usage.TokensOut == nil {
				valid = false
			}
			for _, count := range []int64{u.Input, u.Output, u.CacheRead, u.CacheWrite, u.TotalTokens} {
				if count < 0 || count > (1<<53)-1 {
					valid = false
				}
			}
			input := u.Input + u.CacheRead + u.CacheWrite
			if u.TotalTokens <= 0 || u.TotalTokens != input+u.Output || (usage.TokensIn != nil && *usage.TokensIn != input) || (usage.TokensOut != nil && *usage.TokensOut != u.Output) {
				valid = false
			}
			if usage.CostUSD != nil && h.metadata.Cost != nil {
				rates := h.metadata.Cost
				expected := (float64(u.Input)*rates.Input + float64(u.Output)*rates.Output + float64(u.CacheRead)*rates.CacheRead + float64(u.CacheWrite)*rates.CacheWrite) / 1e6
				if math.IsInf(expected, 0) || math.IsNaN(expected) || math.Abs(*usage.CostUSD-expected) > 1e-9*math.Max(1, expected) {
					valid = false
				}
			}
		} else if usage.TokensIn != nil || usage.TokensOut != nil || usage.CostUSD != nil {
			valid = false
		}
		if valid {
			result.Evidence["agent/usage.json"] = usageBytes
			if usage.Complete {
				result.TokensIn = usage.TokensIn
				result.TokensOut = usage.TokensOut
				result.CostUSD = usage.CostUSD
			}
		}
	}
	recovery, e := sb.ReadFile(ctx, path.Join(req.StateDir, "recovery.json"))
	var recoveryID struct {
		ContractVersion string `json:"contract_version"`
		RunID           string `json:"run_id"`
		Fingerprint     string `json:"fingerprint"`
		Restarts        int    `json:"restarts"`
		Attempts        []struct {
			Attempt     int     `json:"attempt"`
			Code        *int    `json:"code"`
			Signal      *string `json:"signal"`
			ActiveShell bool    `json:"active_shell"`
		} `json:"attempts"`
	}
	if e == nil && len(recovery) <= piSummaryLimit && json.Unmarshal(recovery, &recoveryID) == nil && recoveryID.ContractVersion == PiContractVersion && recoveryID.RunID == req.RunID && recoveryID.Fingerprint == req.Fingerprint && recoveryID.Restarts == terminal.Restarts && len(recoveryID.Attempts) <= terminal.Restarts+1 {
		valid := true
		for i, attempt := range recoveryID.Attempts {
			if attempt.Attempt != i+1 || (attempt.Code != nil && (*attempt.Code < 0 || *attempt.Code > 255)) {
				valid = false
			}
		}
		if valid {
			result.Evidence["agent/recovery.json"] = recovery
		}
	}
	return result, nil
}
