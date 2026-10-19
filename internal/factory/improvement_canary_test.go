package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/harnessprofile"
	"github.com/mitkox/esf/internal/inferencebudget"
	"github.com/mitkox/esf/internal/sandbox"
)

// This adapter exists only in a test binary. The driver sets ESF_CUBE_TEST only
// inside a disposable microVM; CI hosts skip the actual-native-client canary.
type canarySandbox struct{}

func (canarySandbox) ID() string                    { return "native-profile-canary" }
func (canarySandbox) Template() string              { return "cube-native-test" }
func (canarySandbox) Destroy(context.Context) error { return nil }
func (canarySandbox) WriteFile(_ context.Context, path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	return os.WriteFile(path, b, 0600)
}
func (canarySandbox) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}
func (canarySandbox) Execute(ctx context.Context, in sandbox.Command) (sandbox.Execution, error) {
	if in.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, in.Timeout)
		defer cancel()
	}
	var cmd *exec.Cmd
	if in.Script != "" {
		cmd = exec.CommandContext(ctx, "/bin/bash", "-c", in.Script)
	} else {
		cmd = exec.CommandContext(ctx, in.Argv[0], in.Argv[1:]...)
	}
	cmd.Dir = in.Dir
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/root", "OPENCODE_DISABLE_AUTOUPDATE=true"}
	for k, v := range in.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if in.StdinPath != "" {
		f, e := os.Open(in.StdinPath)
		if e != nil {
			return sandbox.Execution{}, e
		}
		defer f.Close()
		cmd.Stdin = f
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	started := time.Now()
	e := cmd.Run()
	code := 0
	if e != nil {
		if x, ok := e.(*exec.ExitError); ok {
			code = x.ExitCode()
		} else {
			return sandbox.Execution{}, e
		}
	}
	return sandbox.Execution{ExitCode: code, Stdout: out.String(), Stderr: stderr.String(), StartedAt: started, CompletedAt: time.Now(), Duration: time.Since(started), TimedOut: ctx.Err() != nil}, nil
}

func TestOpenCode2ActuallyLoadsInstructionProfileAndSkill(t *testing.T) {
	if os.Getenv("ESF_CUBE_TEST") != "1" {
		t.Skip("native-client canary runs only inside the Cube validation driver")
	}
	const binary = "/opt/esf/agents/opencode2"
	if _, e := os.Stat(binary); e != nil {
		t.Fatal("pinned OpenCode2 missing from Cube template:", e)
	}
	cfg := Config{Storage: StorageConfig{DataDir: t.TempDir()}}
	a := &Activities{cfg: cfg}
	s, e := cfg.ProfileStore()
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := harnessprofile.Profile{Schema: harnessprofile.Schema, Binding: harnessprofile.Binding{Scope: "default", Repository: "canary", Harness: "opencode2", Model: "fixture/canary", HarnessDigest: "sha256:ff17011078cfe1d2db04f429c65a5bf9dd86e232cc493c7a03730ecc12a000ff", Template: "cube-native-test"}, Upstream: harnessprofile.Upstream, Author: "canary", Components: map[string]string{"systemprompt.md": "ESF_PROFILE_SENTINEL: load the canary skill for this task.", "skills/canary/SKILL.md": "---\nname: canary\ndescription: native profile activation test\n---\nESF_SKILL_SENTINEL_d18c2b9e\n"}}
	d, e := s.Put(p)
	if e != nil {
		t.Fatal(e)
	}
	skill := "/workspace/.factory/harness-profiles/" + d[7:] + "/skills/canary/SKILL.md"
	var mu sync.Mutex
	seenPrompt, seenSkill := false, false
	requests := 0
	model := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages json.RawMessage `json:"messages"`
			Tools    []struct {
				Function struct {
					Name       string `json:"name"`
					Parameters struct {
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if e := json.NewDecoder(r.Body).Decode(&payload); e != nil {
			t.Error(e)
			http.Error(w, "bad JSON", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		requests++
		seenPrompt = seenPrompt || bytes.Contains(payload.Messages, []byte("ESF_PROFILE_SENTINEL"))
		seenSkill = seenSkill || bytes.Contains(payload.Messages, []byte("ESF_SKILL_SENTINEL_d18c2b9e"))
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(delta any, finish any) {
			b, _ := json.Marshal(map[string]any{"id": "chatcmpl-canary", "object": "chat.completion.chunk", "created": 1, "model": "canary", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		if !seenSkill {
			name, key := "", ""
			for _, tool := range payload.Tools {
				if strings.Contains(strings.ToLower(tool.Function.Name), "read") {
					for k := range tool.Function.Parameters.Properties {
						if strings.Contains(strings.ToLower(k), "path") {
							name, key = tool.Function.Name, k
							break
						}
					}
				}
				if key != "" {
					break
				}
			}
			if name == "" {
				// OpenCode may issue a tool-free title/summary request before
				// the build agent's tool loop. It is not the skill probe.
				emit(map[string]any{"content": "Profile canary"}, "stop")
			} else {
				args, _ := json.Marshal(map[string]string{key: skill})
				emit(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "read-canary", "type": "function", "function": map[string]string{"name": name, "arguments": string(args)}}}}, nil)
				emit(map[string]any{}, "tool_calls")
			}
		} else {
			emit(map[string]any{"role": "assistant", "content": "ESF_PROFILE_ACTIVATED"}, "stop")
		}
		fmt.Fprint(w, "data: {\"id\":\"usage\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":30,\"total_tokens\":130}}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	ledger, e := inferencebudget.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer ledger.Close()
	pricing := harnessprofile.Hash("native-canary-fixture-pricing")
	route := inferencebudget.RouteDigest(model.URL+"/v1", "canary", 1, 1, pricing)
	account := inferencebudget.Account{ID: "native-canary", Scope: "fixture", Model: "canary", LimitMicros: 128, Expires: time.Now().Add(2 * time.Minute), MaxInputTokens: 1000000, MaxOutputTokens: 8192, InputPrice: 1, OutputPrice: 1, RouteDigest: route, PricingEvidence: pricing, MaxRequests: 64}
	if e = ledger.Create(account); e != nil {
		t.Fatal(e)
	}
	tokenKey := []byte(strings.Repeat("fixture-token-key", 3))
	gw := &inferencebudget.Gateway{Ledger: ledger, TokenKey: tokenKey, AdminKey: strings.Repeat("fixture-admin-key", 3), Upstream: model.URL + "/v1", UpstreamKey: "fixture-provider-key", Client: model.Client(), Model: "canary", RouteDigest: route, PricingEvidence: pricing, InputPrice: 1, OutputPrice: 1}
	if e = gw.Validate(); e != nil {
		t.Fatal(e)
	}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read failure", 400)
			return
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(body, &fields) == nil {
			keys := make([]string, 0, len(fields))
			for k := range fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Log("native request fields:", keys)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		gw.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	h, e := agentharness.NewOpenCode(agentharness.OpenCodeOptions{Preinstalled: true, Binary: binary, BinarySHA256: p.Binding.HarnessDigest[7:], Model: "fixture/canary", BaseURL: gateway.URL + "/v1", APIKeyEnv: "FIXTURE_KEY", Timeout: 90 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sb := canarySandbox{}
	if e = h.Provision(ctx, sb); e != nil {
		t.Fatal(e)
	}
	prompt, e := a.applyInstructionProfile(ctx, sb, d, "Read the available canary skill and report completion.")
	if e != nil {
		t.Fatal(e)
	}
	result, e := h.Run(ctx, sb, agentharness.Task{RunID: "profile-canary", Prompt: prompt, RepositoryDir: t.TempDir(), Model: "fixture/canary", Timeout: 90 * time.Second, Env: map[string]string{"FIXTURE_KEY": inferencebudget.Token(tokenKey, account.ID)}})
	if e != nil || !result.Succeeded() {
		t.Fatalf("native client failed: %v exit=%d stdout=%s stderr=%s", e, result.ExitCode, result.Stdout, result.Stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if !seenPrompt || !seenSkill || requests < 2 {
		t.Fatalf("native component activation missing: prompt=%v skill=%v requests=%d stdout=%s", seenPrompt, seenSkill, requests, result.Stdout)
	}
	settled, e := ledger.Get(account.ID)
	if e != nil || settled.Revoked || len(settled.Calls) < 2 {
		t.Fatal("native requests did not pass the bounded gateway", settled, e)
	}
	for _, call := range settled.Calls {
		if call.State != "settled" {
			t.Fatal("native usage was not authoritative", call)
		}
	}
	t.Log("Pinned OpenCode2 consumed the profile and read its mounted skill through the actual tool loop and bounded gateway; provider was a deterministic local TLS fixture.")
}
