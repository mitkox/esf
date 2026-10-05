package agentharness

import (
	"context"
	"encoding/json"
	"path"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

const piTestMetadata = `{"id":"test-model","name":"Test","api":"openai-completions","reasoning":false,"input":["text"],"contextWindow":32768,"maxTokens":1024,"pricingKnown":false,"thinkingLevels":["off"]}`

func piTestOptions() PiOptions {
	return PiOptions{Binary: "/opt/esf/agents/pi-runner.mjs", BinarySHA256: strings.Repeat("a", 64), RuntimeSHA256: strings.Repeat("b", 64), Preinstalled: true, Provider: "local", BaseURL: "http://gateway.internal:8000/v1", Model: "test-model", CatalogCache: "model.json", Timeout: time.Minute, ReadFile: func(string) ([]byte, error) { return []byte(piTestMetadata), nil }}
}

func TestPiFixedTransportAndCredentialIsolation(t *testing.T) {
	opts := piTestOptions()
	opts.EgressManaged = true
	opts.Provider = "openai"
	opts.BaseURL = "https://api.openai.com/v1"
	opts.LookupEnv = func(string) (string, bool) { t.Fatal("host credential accessed in managed mode"); return "", false }
	h, err := NewPi(opts)
	if err != nil {
		t.Fatal(err)
	}
	f := sandbox.NewFake()
	sb, _ := f.Create(context.Background(), sandbox.Spec{})
	task := Task{RunID: "run-1", RepositoryDir: "/workspace/repository", Prompt: `Fix $(whoami); "quotes" and <text>`, Env: map[string]string{"UNRELATED_SECRET": "secret", "ESF_PI_API_KEY": "attacker", "NODE_OPTIONS": "--import evil"}}
	f.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		if !reflect.DeepEqual(cmd.Argv, []string{PiRuntimePath, "--use-system-ca", opts.Binary}) {
			t.Fatalf("argv=%v", cmd.Argv)
		}
		if len(cmd.Env) != 1 || cmd.Env[piKeyEnvironment] != "cube-egress-managed-placeholder" {
			t.Fatalf("environment=%v", cmd.Env)
		}
		data, _ := sb.ReadFile(context.Background(), cmd.StdinPath)
		var req piRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatal(err)
		}
		if req.Prompt != task.Prompt || req.RunID != task.RunID || req.TimeoutMS != 60000 {
			t.Fatalf("request=%+v", req)
		}
		terminal := piTerminal{ContractVersion: PiContractVersion, RunID: req.RunID, RequestID: req.RequestID, Fingerprint: req.Fingerprint, Model: req.Model, Status: "completed"}
		data, _ = json.Marshal(terminal)
		_ = sb.WriteFile(context.Background(), path.Join(req.StateDir, "terminal.json"), data)
		return sandbox.Execution{ExitCode: 0}, nil
	}
	result, err := h.Run(context.Background(), sb, task)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Succeeded() || strings.Contains(result.CommandLine, task.Prompt) {
		t.Fatalf("result=%+v", result)
	}
	if len(result.Evidence["agent/pi-terminal.json"]) == 0 {
		t.Fatal("terminal evidence missing")
	}
	req1, _ := h.request(task)
	req2, _ := h.request(task)
	if req1.RequestID != req2.RequestID || req1.Fingerprint != req2.Fingerprint {
		t.Fatal("unstable submission identity")
	}
	task.Prompt += " changed"
	req2, _ = h.request(task)
	if req1.Fingerprint == req2.Fingerprint || req1.RequestID == req2.RequestID {
		t.Fatal("changed task reused identity")
	}
}

func TestPiExitZeroRequiresMatchingTerminal(t *testing.T) {
	for _, kind := range []string{"missing", "malformed", "wrong identity"} {
		t.Run(kind, func(t *testing.T) {
			h, _ := NewPi(piTestOptions())
			f := sandbox.NewFake()
			sb, _ := f.Create(context.Background(), sandbox.Spec{})
			task := Task{RunID: "run", RepositoryDir: "/repo", Prompt: "task"}
			req, _ := h.request(task)
			if kind != "missing" {
				data := []byte(`{`)
				if kind == "wrong identity" {
					data = []byte(`{"contract_version":"esf/pi/v1","status":"completed","run_id":"other"}`)
				}
				_ = sb.WriteFile(context.Background(), path.Join(req.StateDir, "terminal.json"), data)
			}
			result, err := h.Run(context.Background(), sb, task)
			if err != nil || result.Succeeded() {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestPiProvisionRefusesRuntimeAndBundleDigestMismatch(t *testing.T) {
	for _, which := range []string{"runtime", "bundle"} {
		t.Run(which, func(t *testing.T) {
			h, _ := NewPi(piTestOptions())
			f := sandbox.NewFake()
			sb, _ := f.Create(context.Background(), sandbox.Spec{})
			f.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
				if cmd.Argv[0] != "sha256sum" {
					t.Fatalf("executed runner before digest verification: %v", cmd.Argv)
				}
				digest := strings.Repeat("a", 64)
				if cmd.Argv[1] == PiRuntimePath {
					digest = strings.Repeat("b", 64)
				}
				if (which == "runtime" && cmd.Argv[1] == PiRuntimePath) || (which == "bundle" && cmd.Argv[1] != PiRuntimePath) {
					digest = strings.Repeat("c", 64)
				}
				return sandbox.Execution{ExitCode: 0, Stdout: digest + " file"}, nil
			}
			if h.Provision(context.Background(), sb) == nil {
				t.Fatal("digest mismatch accepted")
			}
		})
	}
}

func TestPiRejectsModelPolicyAndInvalidConfiguration(t *testing.T) {
	h, _ := NewPi(piTestOptions())
	for _, e := range []ModelEndpoint{{Model: "other"}, {Provider: "other"}, {BaseURL: "http://other/v1"}, {APIKeyEnv: "OTHER_KEY"}} {
		if h.ValidateModelEndpoint(e) == nil {
			t.Fatalf("accepted %+v", e)
		}
	}
	if h.ValidateModel("other") == nil {
		t.Fatal("model override accepted")
	}
	for _, mutate := range []func(*PiOptions){
		func(o *PiOptions) { o.Preinstalled = false }, func(o *PiOptions) { o.RuntimeSHA256 = "" }, func(o *PiOptions) { o.BinarySHA256 = "bad" },
		func(o *PiOptions) { o.API = "other" }, func(o *PiOptions) { n := 2; o.MaxProcessRestarts = &n }, func(o *PiOptions) { o.CatalogCache = "" },
		func(o *PiOptions) { o.ThinkingLevel = "high" }, func(o *PiOptions) {
			o.ReadFile = func(string) ([]byte, error) { return []byte(piTestMetadata + ` {}`), nil }
		},
		func(o *PiOptions) {
			o.ReadFile = func(string) ([]byte, error) {
				return []byte(strings.Replace(piTestMetadata, `"contextWindow":32768`, `"contextWindow":0`, 1)), nil
			}
		},
		func(o *PiOptions) {
			o.ReadFile = func(string) ([]byte, error) {
				return []byte(strings.Replace(piTestMetadata, `"contextWindow":32768`, `"contextWindow":9007199254740992`, 1)), nil
			}
		},
		func(o *PiOptions) {
			o.ReadFile = func(string) ([]byte, error) {
				return []byte(strings.TrimSuffix(piTestMetadata, "}") + `,"compat":"invalid"}`), nil
			}
		},
		func(o *PiOptions) {
			o.ReadFile = func(string) ([]byte, error) {
				return []byte(strings.TrimSuffix(piTestMetadata, "}") + `,"compat":null}`), nil
			}
		},
		func(o *PiOptions) {
			o.ReadFile = func(string) ([]byte, error) {
				return []byte(strings.TrimSuffix(piTestMetadata, "}") + `,"cost":{"input":0,"output":0,"cacheRead":0}}`), nil
			}
		},
		func(o *PiOptions) {
			o.ReadFile = func(string) ([]byte, error) {
				return []byte(strings.TrimSuffix(piTestMetadata, "}") + `,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":null}}`), nil
			}
		},
	} {
		opts := piTestOptions()
		mutate(&opts)
		if _, err := NewPi(opts); err == nil {
			t.Fatalf("accepted invalid options %+v", opts)
		}
	}
}

func TestPiPinnedCatalogAndUsageValidation(t *testing.T) {
	opts := piTestOptions()
	opts.CatalogCache = ""
	opts.Provider = "openai"
	opts.Model = "gpt-4.1"
	opts.API = "openai-responses"
	opts.APIKeyEnv = "TEST_KEY"
	if _, err := NewPi(opts); err != nil {
		t.Fatalf("pinned catalog model: %v", err)
	}
	for _, kind := range []string{"complete", "partial", "inconsistent"} {
		t.Run(kind, func(t *testing.T) {
			h, _ := NewPi(piTestOptions())
			f := sandbox.NewFake()
			sb, _ := f.Create(context.Background(), sandbox.Spec{})
			task := Task{RunID: "run", RepositoryDir: "/repo", Prompt: "task"}
			req, _ := h.request(task)
			terminal, _ := json.Marshal(piTerminal{ContractVersion: PiContractVersion, RunID: req.RunID, RequestID: req.RequestID, Fingerprint: req.Fingerprint, Model: req.Model, Status: "completed"})
			_ = sb.WriteFile(context.Background(), path.Join(req.StateDir, "terminal.json"), terminal)
			input, output := int64(12), int64(5)
			u := piUsage{ContractVersion: PiContractVersion, RunID: req.RunID, Fingerprint: req.Fingerprint, Complete: true, TokensIn: &input, TokensOut: &output, CostBasis: "unavailable", Models: map[string]piModelUsage{"local/test-model": {Input: 10, Output: 5, CacheRead: 2, TotalTokens: 17}}}
			if kind == "partial" {
				u.Complete = false
				u.TokensIn = nil
				u.TokensOut = nil
			}
			if kind == "inconsistent" {
				input = 13
			}
			data, _ := json.Marshal(u)
			_ = sb.WriteFile(context.Background(), path.Join(req.StateDir, "usage.json"), data)
			result, err := h.Run(context.Background(), sb, task)
			if err != nil || !result.Succeeded() {
				t.Fatalf("run=%+v err=%v", result, err)
			}
			if kind == "complete" {
				if result.TokensIn == nil || *result.TokensIn != 12 || result.TokensOut == nil || *result.TokensOut != 5 {
					t.Fatal("valid committed usage lost")
				}
			} else if result.TokensIn != nil || result.TokensOut != nil {
				t.Fatal("partial or inconsistent usage presented as a total")
			}
			if result.CostUSD != nil {
				t.Fatal("unknown prices presented as a cost")
			}
		})
	}
}
