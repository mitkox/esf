package turnexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mitkox/esf/internal/sandbox"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const serviceToken = "private-service-token-longer-than-32"
const ownerToken = "private-owner-token-longer-than-32"

func profiles() map[string]Profile {
	return map[string]Profile{"claude": {Template: "approved-template", Command: []string{"claude", "-p", "--output-format", "stream-json"}, Harness: "claude-code", TimeoutSeconds: 10}}
}
func fixture(t *testing.T) (*Service, *sandbox.Fake) {
	t.Helper()
	f := sandbox.NewFake()
	f.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
		if len(cmd.Argv) > 1 && cmd.Argv[1] == "/tmp/esf-run.py" {
			ids := f.Created()
			sb, _ := f.Reattach(context.Background(), ids[len(ids)-1])
			_ = sb.WriteFile(context.Background(), "/tmp/esf-stdout", []byte(`{"type":"result","result":"hello","session_id":"harness-123","total_cost_usd":0.1,"usage":{"input_tokens":12,"output_tokens":3}}`))
		}
		if len(cmd.Argv) > 2 && cmd.Argv[2] == collectScript {
			ids := f.Created()
			sb, _ := f.Reattach(context.Background(), ids[len(ids)-1])
			b, _ := json.Marshal(Manifest{Files: []File{{Path: "binary.dat", Content: []byte{0, 255, 1, 128}}, {Path: ".home/.claude/session.json", Content: []byte("state")}}})
			_ = sb.WriteFile(context.Background(), "/tmp/esf-checkpoint.json", b)
		}
		return sandbox.Execution{ExitCode: 0}, nil
	}
	s, err := Open(context.Background(), t.TempDir(), serviceToken, profiles(), f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, f
}
func call(s *Service, method, path string, v any) *httptest.ResponseRecorder {
	var b []byte
	if v != nil {
		b, _ = json.Marshal(v)
	}
	r := httptest.NewRequest(method, "/v1/"+path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+serviceToken)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func request(id string) Request {
	return Request{ExecutionID: id, Profile: "claude", SpecSHA: strings.Repeat("a", 64), Prompt: "hello $(do not execute)", OwnerToken: ownerToken}
}
func waitResult(t *testing.T, s *Service, id string) Result {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := call(s, "GET", "executions/"+id+"/result", nil)
		if w.Code == 200 {
			var result Result
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("result timeout")
	return Result{}
}
func TestIdempotencyIdentityAndBinaryCheckpoint(t *testing.T) {
	s, f := fixture(t)
	req := request("one")
	for range 2 {
		if w := call(s, "POST", "executions", req); w.Code != 202 && w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	result := waitResult(t, s, "one")
	if result.Result["status"] != "completed" || result.Checkpoint == "" {
		t.Fatal(result)
	}
	if len(f.Created()) != 1 || len(f.Live()) != 0 {
		t.Fatal("duplicate or leaked sandbox")
	}
	manifest, err := s.manifest(result.Checkpoint)
	if err != nil || manifest.HarnessSessionID != "harness-123" || !bytes.Equal(manifest.Files[0].Content, []byte{0, 255, 1, 128}) {
		t.Fatal(manifest, err)
	}
	req.Prompt = "changed"
	if w := call(s, "POST", "executions", req); w.Code != 409 {
		t.Fatal(w.Code)
	}
	req = request("two")
	req.Checkpoint = result.Checkpoint
	if w := call(s, "POST", "executions", req); w.Code != 202 {
		t.Fatal(w.Code)
	}
	second := waitResult(t, s, "two")
	if second.Result["status"] != "completed" {
		t.Fatal(second)
	}
	if len(f.Created()) != 2 {
		t.Fatal("must create fresh VM")
	}
	events := call(s, "GET", "executions/one/events?after=1", nil)
	var list []Event
	_ = json.Unmarshal(events.Body.Bytes(), &list)
	if len(list) < 2 || list[0].Seq != 2 {
		t.Fatal(events.Body.String())
	}
	disk, err := os.ReadFile(filepath.Join(s.root, "executions", "one.json"))
	if err != nil || bytes.Contains(disk, []byte(ownerToken)) {
		t.Fatal("raw ownership token persisted", err)
	}
}
func TestRejectUnsafeRequestsAndOwnership(t *testing.T) {
	s, _ := fixture(t)
	unauth := httptest.NewRecorder()
	s.ServeHTTP(unauth, httptest.NewRequest("GET", "/v1/executions/one", nil))
	if unauth.Code != 401 {
		t.Fatal(unauth.Code)
	}
	b, _ := json.Marshal(request("one"))
	var raw map[string]any
	_ = json.Unmarshal(b, &raw)
	raw["command"] = []string{"sh", "-c", "bad"}
	if w := call(s, "POST", "executions", raw); w.Code != 400 {
		t.Fatal(w.Code)
	}
	raw["command"] = nil
	if w := call(s, "POST", "executions", raw); w.Code != 400 {
		t.Fatal(w.Code)
	}
	bad := request("../escape")
	if w := call(s, "POST", "executions", bad); w.Code != 400 {
		t.Fatal(w.Code)
	}
	req := request("one")
	_ = call(s, "POST", "executions", req)
	_ = waitResult(t, s, "one")
	if w := call(s, "POST", "executions/one/cancel", map[string]string{"owner_token": "wrong"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, p := range []string{"../escape", "/absolute", "a/../b", ".", "a\\b"} {
		if _, err := s.putManifest(Manifest{Files: []File{{Path: p, Content: []byte("bad")}}}); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	if _, err := s.putManifest(Manifest{Files: []File{{Path: "a", Content: []byte("x")}, {Path: "a/b", Content: []byte("y")}}}); err == nil {
		t.Fatal("accepted overlapping files")
	}
}
func TestManifestCorruptionAndExclusiveState(t *testing.T) {
	s, f := fixture(t)
	sha, err := s.putManifest(Manifest{Files: []File{{Path: "a", Content: []byte("a")}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "blobs", sha+".json"), []byte(`{"files":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.manifest(sha); err == nil {
		t.Fatal("accepted corrupt manifest")
	}
	if _, err := Open(context.Background(), s.root, serviceToken, profiles(), f); err == nil {
		t.Fatal("allowed two owners of state")
	}
}
func TestRestartFailsWithoutReplayAndDestroys(t *testing.T) {
	f := sandbox.NewFake()
	root := t.TempDir()
	s, err := Open(context.Background(), root, serviceToken, profiles(), f)
	if err != nil {
		t.Fatal(err)
	}
	req := request("interrupted")
	req.OwnerToken = ""
	sb, err := f.Create(context.Background(), sandbox.Spec{Metadata: map[string]string{"esf.turn.id": req.ExecutionID, "esf.turn.service": digest([]byte(root))}})
	if err != nil {
		t.Fatal(err)
	}
	r := &record{Status: Status{ExecutionID: req.ExecutionID, State: "running"}, Request: req, StartedAt: time.Now(), SandboxID: sb.ID(), Events: []Event{}}
	if err = s.persist(r); err != nil {
		t.Fatal(err)
	}
	s.Close()
	recovered, err := Open(context.Background(), root, serviceToken, profiles(), f)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	result := waitResult(t, recovered, req.ExecutionID)
	if result.Result["status"] != "failed" || len(f.Created()) != 1 || len(f.Live()) != 0 {
		t.Fatal(result, "must fail closed without replay")
	}
}
func TestCancelAndLeaseExpiry(t *testing.T) {
	for _, mode := range []string{"cancel", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s, f := fixture(t)
			started := make(chan struct{})
			release := make(chan struct{})
			base := f.ExecuteFunc
			f.ExecuteFunc = func(cmd sandbox.Command) (sandbox.Execution, error) {
				if len(cmd.Argv) > 1 && cmd.Argv[1] == "/tmp/esf-run.py" {
					close(started)
					<-release
				}
				return base(cmd)
			}
			_ = call(s, "POST", "executions", request("one"))
			<-started
			if mode == "cancel" {
				w := call(s, "POST", "executions/one/cancel", map[string]string{"owner_token": ownerToken})
				if w.Code != 200 {
					t.Fatal(w.Code)
				}
			} else {
				s.mu.Lock()
				s.records["one"].Status.LeaseExpiresAt = time.Now().Add(-time.Second)
				s.mu.Unlock()
				if w := call(s, "POST", "executions/one/renew", map[string]string{"owner_token": ownerToken}); w.Code != 409 {
					t.Fatal(w.Code)
				}
				time.Sleep(1100 * time.Millisecond)
			}
			close(release)
			result := waitResult(t, s, "one")
			if result.Result["status"] != "cancelled" || len(f.Live()) != 0 {
				t.Fatal(result)
			}
		})
	}
}

func TestPermissionBindingAndMCPBridge(t *testing.T) {
	s, f := fixture(t)
	ctx := context.Background()
	sb, err := f.Create(ctx, sandbox.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Destroy(ctx)
	req := request("permission")
	req.OwnerToken = ""
	rec := &record{Status: Status{ExecutionID: req.ExecutionID, State: "running", LeaseExpiresAt: time.Now().Add(time.Minute)}, Request: req, OwnerHash: digest([]byte(ownerToken)), Events: []Event{}, cancel: func() {}}
	s.mu.Lock()
	s.records[req.ExecutionID] = rec
	s.mu.Unlock()
	operation := []byte(`{"id":"approval-one","tool_name":"Bash","input":{"command":"echo exact"}}`)
	_ = sb.WriteFile(ctx, "/tmp/esf-permission.json", operation)
	if err = s.pollPermission(ctx, sb, rec); err != nil {
		t.Fatal(err)
	}
	decision := map[string]string{"owner_token": ownerToken, "permission_id": "approval-one", "operation_digest": "wrong", "decision": "allow"}
	if w := call(s, "POST", "executions/permission/permissions", decision); w.Code != 409 {
		t.Fatal(w.Code)
	}
	decision["operation_digest"] = digest(operation)
	if w := call(s, "POST", "executions/permission/permissions", decision); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if err = s.pollPermission(ctx, sb, rec); err != nil {
		t.Fatal(err)
	}
	data, err := sb.ReadFile(ctx, "/tmp/esf-decision.json")
	if err != nil || !bytes.Contains(data, []byte(`"decision":"allow"`)) {
		t.Fatal(string(data), err)
	}
	if w := call(s, "POST", "executions/permission/permissions", decision); w.Code != 200 {
		t.Fatal(w.Code)
	}
	decision["decision"] = "deny"
	if w := call(s, "POST", "executions/permission/permissions", decision); w.Code != 409 {
		t.Fatal(w.Code)
	}
	rec.Status.State = "failed"
}
func TestPermissionPythonProtocol(t *testing.T) {
	for _, p := range []string{"/tmp/esf-permission.json", "/tmp/esf-permission.new", "/tmp/esf-decision.json"} {
		_ = os.Remove(p)
		defer os.Remove(p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", permissionScript)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()
	_, _ = stdin.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"))
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "protocolVersion") {
		t.Fatal(line, err)
	}
	_, _ = stdin.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"echo $(must remain data)"}}}}` + "\n"))
	var raw []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err = os.ReadFile("/tmp/esf-permission.json")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	var op map[string]any
	_ = json.Unmarshal(raw, &op)
	decision, _ := json.Marshal(map[string]any{"id": op["id"], "operation_digest": digest(raw), "decision": "allow"})
	if err = os.WriteFile("/tmp/esf-decision.json", decision, 0600); err != nil {
		t.Fatal(err)
	}
	line, err = reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "updatedInput") || !strings.Contains(line, "must remain data") {
		t.Fatal(line, err)
	}
}

func TestPolicyReceiptSelectionAndBudget(t *testing.T) {
	f := sandbox.NewFake()
	p := profiles()["claude"]
	p.Command = []string{"claude"}
	p.TimeoutSeconds = 10
	var policy TurnPolicy
	if err := json.Unmarshal([]byte(`{"harness":{"name":"claude-code","permission_mode":"default","model":"approved-model"},"tools":{"allow":[],"deny":["Bash"]},"sandbox":{"network":"none","timeout_seconds":10,"memory":"4g","cpus":"2"}}`), &policy); err != nil {
		t.Fatal(err)
	}
	no := false
	p.Network.AllowInternet = &no
	p.TurnPolicy = &policy
	s, err := Open(context.Background(), t.TempDir(), serviceToken, map[string]Profile{"policy": p}, f)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	receipt := call(s, "GET", "profiles/policy", nil)
	if receipt.Code != 200 {
		t.Fatal(receipt.Body.String())
	}
	req := request("policy-one")
	req.Profile = "policy"
	if w := call(s, "POST", "executions", req); w.Code != 400 {
		t.Fatal("missing policy accepted")
	}
	req.PolicySHA = policyDigest(&policy)
	req.ProfileSHA = strings.Repeat("0", 64)
	if w := call(s, "POST", "executions", req); w.Code != 409 {
		t.Fatal("changed profile accepted")
	}
	req.ProfileSHA = digestJSON(p)
	req.Metadata = map[string]string{"GITLAB_TOKEN": "not allowed"}
	if w := call(s, "POST", "executions", req); w.Code != 400 {
		t.Fatal("secret metadata accepted")
	}
	req.Metadata = map[string]string{"SF_STEP": "one"}
	zero := 0.0
	req.MaxCostUSD = &zero
	if w := call(s, "POST", "executions", req); w.Code != 400 {
		t.Fatal("zero cap accepted")
	}
	p.SelectionOnly = true
	if validatePolicy(p) == nil {
		t.Fatal("combined selector/workflow policy accepted")
	}
	p.TurnPolicy = nil
	p.InteractivePermissions = true
	if _, err = Open(context.Background(), t.TempDir(), serviceToken, map[string]Profile{"bad": p}, f); err == nil {
		t.Fatal("selector interactive bypass accepted")
	}
}
func TestUnknownModelPricingIsNotCostCoverage(t *testing.T) {
	s, _ := fixture(t)
	r := &record{Status: Status{ExecutionID: "unknown-price", State: "running"}, Request: request("unknown-price"), StartedAt: time.Now(), Events: []Event{}}
	s.finish(r, context.Background(), sandbox.Execution{ExitCode: 0, Stdout: `{"type":"result","result":"ok","total_cost_usd":0.008,"usage":{"input_tokens":1400,"output_tokens":40}}`, Stderr: `[claude-code:unrecognized_model] {"model":"minimax-m3"}`}, Manifest{Files: []File{}}, nil, "", true)
	usage := r.Result.Result["usage"].(map[string]any)
	if _, ok := usage["cost_usd"]; ok {
		t.Fatal("unknown-model fallback price claimed as covered")
	}
	if r.Result.Result["signals"].(map[string]any)["cost_usd"] != nil {
		t.Fatal("unknown cost must remain null")
	}
}

type lostCreateProvider struct{ *sandbox.Fake }

func (p lostCreateProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	_, err := p.Fake.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	return nil, errors.New("lost create response")
}
func TestLostCreateResponseReconcilesByMetadata(t *testing.T) {
	f := sandbox.NewFake()
	s, err := Open(context.Background(), t.TempDir(), serviceToken, profiles(), lostCreateProvider{f})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if w := call(s, "POST", "executions", request("lost-response")); w.Code != 202 {
		t.Fatal(w.Code)
	}
	result := waitResult(t, s, "lost-response")
	cleanup := result.Result["sandbox"].(map[string]any)["cleanup"].(map[string]any)
	if len(f.Created()) != 1 || len(f.Live()) != 0 || cleanup["verified"] != true || result.Result["status"] != "failed" {
		t.Fatal(result, "lost response leaked or reran sandbox")
	}
}

func TestExactResourcePolicyParsing(t *testing.T) {
	p := &TurnPolicy{}
	p.Sandbox.Memory = "2000m"
	p.Sandbox.CPUs = "0.5"
	limits, err := policyLimits(p)
	if err != nil || limits.MemoryMiB != 2000 || limits.CPUMilli != 500 {
		t.Fatal(limits, err)
	}
	for _, bad := range []string{"4gb", "0m", "-1g", "4;echo g"} {
		p.Sandbox.Memory = bad
		if _, err = policyLimits(p); err == nil {
			t.Fatal("invalid memory", bad)
		}
	}
	p.Sandbox.Memory = "4g"
	for _, bad := range []string{"0", "0.0001", "NaN", "999"} {
		p.Sandbox.CPUs = bad
		if _, err = policyLimits(p); err == nil {
			t.Fatal("invalid CPU", bad)
		}
	}
}
