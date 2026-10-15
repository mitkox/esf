package turnexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

const workspace = "/workspace/sf"

// Python is part of the operator-approved template. This wrapper drains output
// incrementally into a bounded spool, and terminates the process at the bound.
// Client data is never interpolated into code, shell, or argv.
const runnerScript = `import json, os, subprocess, sys, selectors
cfg=json.load(open('/tmp/esf-command.json'))
with open('/tmp/esf-prompt','rb') as prompt, open('/tmp/esf-stdout','wb',buffering=0) as stdout, open('/tmp/esf-stderr','wb',buffering=0) as stderr:
 p=subprocess.Popen(cfg['argv'],cwd='/workspace/sf',stdin=prompt,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 selector=selectors.DefaultSelector()
 selector.register(p.stdout,selectors.EVENT_READ,stdout)
 selector.register(p.stderr,selectors.EVENT_READ,stderr)
 total=0
 while selector.get_map():
  for key,_ in selector.select():
   chunk=os.read(key.fileobj.fileno(),4096)
   if not chunk:
    selector.unregister(key.fileobj);continue
   total+=len(chunk)
   if total>2097152:
    p.kill();p.wait();sys.exit(125)
   key.data.write(chunk)
 sys.exit(p.wait())
`
const collectScript = `import os,json,base64,stat
root='/workspace/sf'; files=[];total=0
for directory,dirs,names in os.walk(root,followlinks=False):
 for name in dirs+names:
  p=os.path.join(directory,name);mode=os.lstat(p).st_mode
  if stat.S_ISLNK(mode) or not (stat.S_ISDIR(mode) or stat.S_ISREG(mode)): raise RuntimeError('unsupported checkpoint file')
 for name in sorted(names):
  p=os.path.join(directory,name)
  with open(p,'rb') as f: b=f.read(16777217)
  total+=len(b)
  if total>16777216 or len(files)>=4096: raise RuntimeError('checkpoint limit exceeded')
  files.append({'path':os.path.relpath(p,root),'content_base64':base64.b64encode(b).decode()})
data=json.dumps({'files':files},separators=(',',':')).encode()
if len(data)>16777216: raise RuntimeError('checkpoint encoded limit exceeded')
with open('/tmp/esf-checkpoint.json','wb') as f: f.write(data)
`

func (s *Service) run(ctx context.Context, r *record, p Profile) {
	defer s.wg.Done()
	defer r.cancel()
	leaseDone := make(chan struct{})
	defer close(leaseDone)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-leaseDone:
				return
			case <-ticker.C:
				s.mu.Lock()
				expired := !time.Now().Before(r.Status.LeaseExpiresAt)
				if expired {
					r.Status.Error = "execution ownership lease expired"
					r.cancel()
				}
				s.mu.Unlock()
				if expired {
					return
				}
			}
		}
	}()
	env := map[string]string{"HOME": workspace + "/.home"}
	if p.Harness == "claude-code" {
		env["DISABLE_AUTOUPDATER"] = "1"
		env["DISABLE_TELEMETRY"] = "1"
		env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
	}
	if p.ModelBaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = p.ModelBaseURL
	}
	for key, value := range r.Request.Metadata {
		env[key] = value
	}
	for key, ref := range p.EnvRefs {
		env[key] = os.Getenv(ref)
	}
	limits := sandbox.Limits{}
	if p.TurnPolicy != nil {
		limits, _ = policyLimits(p.TurnPolicy)
	}
	sb, err := s.provider.Create(ctx, sandbox.Spec{Template: p.Template, Env: env, IdleTimeout: time.Duration(p.TimeoutSeconds+60) * time.Second, Network: p.Network, Limits: limits, Metadata: map[string]string{"esf.turn.id": r.Request.ExecutionID, "esf.turn.service": digest([]byte(s.root)), "sf.spec_sha": r.Request.SpecSHA}})
	if err != nil {
		s.finish(r, ctx, sandbox.Execution{}, Manifest{}, nil, "sandbox creation failed", s.cleanupIdentity(r.Request.ExecutionID))
		return
	}
	s.mu.Lock()
	r.SandboxID = sb.ID()
	r.Status.State = "running"
	s.event(r, "state", map[string]any{"status": "running", "sandbox_id": sb.ID()})
	err = s.persist(r)
	s.mu.Unlock()
	var outcome sandbox.Execution
	var checkpoint Manifest
	var initial Manifest
	failure := ""
	if p.TurnPolicy != nil {
		expected, _ := policyLimits(p.TurnPolicy)
		validator, ok := s.provider.(interface {
			ValidateResources(context.Context, string, sandbox.Limits) error
		})
		if !ok {
			failure = "provider cannot verify compiled resource policy"
		} else if e := validator.ValidateResources(ctx, sb.ID(), expected); e != nil {
			failure = "compiled resource policy not satisfied: " + e.Error()
		}
	}
	if data := s.harnesses[r.Request.Profile]; data != nil && failure == "" {
		if e := sb.WriteFile(ctx, "/tmp/esf-harness", data); e != nil {
			failure = "stage pinned harness artifact failed"
		} else {
			out, e := sb.Execute(ctx, sandbox.Command{Argv: []string{"chmod", "0555", "/tmp/esf-harness"}, Timeout: 30 * time.Second})
			if e != nil || !out.Succeeded() {
				failure = "prepare pinned harness artifact failed"
			}
		}
	}
	if err != nil {
		failure = "persist sandbox identity failed"
	} else if failure == "" {
		initial, err = s.restore(ctx, sb, r.Request)
		if err != nil {
			failure = "restore manifest failed"
		}
	}
	if failure == "" {
		argv := policyCommand(p)
		if p.Harness == "claude-code" {
			cap := p.MaxCostUSD
			if cap == 0 {
				cap = 1
			}
			if r.Request.MaxCostUSD != nil && *r.Request.MaxCostUSD < cap {
				cap = *r.Request.MaxCostUSD
			}
			argv = append(argv, "--max-budget-usd", strconv.FormatFloat(cap, 'f', -1, 64))
		}
		if p.SelectionOnly {
			argv = append(argv, "-p", "--verbose", "--output-format", "stream-json", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--setting-sources", "", "--restricted", "--max-turns", "1")
		}
		if p.Harness == "claude-code" && initial.HarnessSessionID != "" {
			argv = append(argv, "--resume", initial.HarnessSessionID)
		}
		if p.InteractivePermissions || len(p.MCPServers) > 0 {
			servers := map[string]any{}
			for name, server := range p.MCPServers {
				servers[name] = map[string]any{"type": "http", "url": server.URL, "headers": map[string]string{"Authorization": "Bearer " + os.Getenv(server.BearerEnvRef)}}
			}
			if p.InteractivePermissions {
				servers["sf_gate"] = map[string]any{"command": "python3", "args": []string{"/tmp/esf-permission.py"}}
				if e := sb.WriteFile(ctx, "/tmp/esf-permission.py", []byte(permissionScript)); e != nil {
					failure = "stage permission bridge failed"
				}
				argv = append(argv, "--permission-prompt-tool", "mcp__sf_gate__approve")
			}
			config, _ := json.Marshal(map[string]any{"mcpServers": servers})
			if e := sb.WriteFile(ctx, "/tmp/esf-mcp.json", config); e != nil {
				failure = "stage MCP config failed"
			}
			argv = append(argv, "--mcp-config", "/tmp/esf-mcp.json", "--strict-mcp-config")
		}
		command, _ := json.Marshal(map[string]any{"argv": argv})
		for _, f := range []struct {
			path string
			data []byte
		}{{"/tmp/esf-command.json", command}, {"/tmp/esf-prompt", []byte(r.Request.Prompt)}, {"/tmp/esf-run.py", []byte(runnerScript)}} {
			if err = sb.WriteFile(ctx, f.path, f.data); err != nil {
				failure = "stage execution failed"
				break
			}
		}
	}
	if failure == "" {
		type executed struct {
			out sandbox.Execution
			err error
		}
		done := make(chan executed, 1)
		go func() {
			out, e := sb.Execute(ctx, sandbox.Command{Argv: []string{"python3", "/tmp/esf-run.py"}, Dir: workspace, Timeout: time.Duration(p.TimeoutSeconds) * time.Second, Description: "sf profile turn"})
			done <- executed{out, e}
		}()
		ticker := time.NewTicker(time.Second)
		offsets := map[string]int{"stdout": 0, "stderr": 0}
		poll := func() {
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if p.InteractivePermissions {
				if e := s.pollPermission(readCtx, sb, r); e != nil {
					failure = "permission bridge failed"
					r.cancel()
					return
				}
			}
			for _, stream := range []string{"stdout", "stderr"} {
				data, e := sb.ReadFile(readCtx, "/tmp/esf-"+stream)
				if e != nil {
					continue
				}
				if len(data) > maxEventBytes {
					failure = "execution output limit exceeded"
					r.cancel()
					return
				}
				if len(data) > offsets[stream] {
					s.mu.Lock()
					s.event(r, stream, map[string]any{"text": string(data[offsets[stream]:])})
					e = s.persist(r)
					s.mu.Unlock()
					offsets[stream] = len(data)
					if e != nil {
						failure = "persist execution event failed"
						r.cancel()
						return
					}
				}
			}
		}
	loop:
		for {
			select {
			case ex := <-done:
				outcome = ex.out
				err = ex.err
				poll()
				break loop
			case <-ticker.C:
				poll()
			}
		}
		ticker.Stop()
		if err != nil {
			failure = "sandbox command failed"
		}
		if data, e := sb.ReadFile(ctx, "/tmp/esf-stdout"); e == nil {
			outcome.Stdout = string(data)
		}
		if data, e := sb.ReadFile(ctx, "/tmp/esf-stderr"); e == nil {
			outcome.Stderr = string(data)
		}
		if outcome.ExitCode == 125 {
			failure = "execution output limit exceeded"
		}
	}
	if failure == "" && ctx.Err() == nil {
		collect, e := sb.Execute(ctx, sandbox.Command{Argv: []string{"python3", "-c", collectScript}, Timeout: 30 * time.Second, Description: "persist binary checkpoint"})
		if e != nil || !collect.Succeeded() {
			failure = "checkpoint collection failed"
		} else {
			data, e := sb.ReadFile(ctx, "/tmp/esf-checkpoint.json")
			if e != nil || len(data) > MaxManifestBytes {
				failure = "checkpoint read failed"
			} else if e = json.Unmarshal(data, &checkpoint); e != nil {
				failure = "checkpoint manifest invalid"
			} else if e = validateManifest(checkpoint); e != nil {
				failure = "checkpoint manifest invalid"
			}
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	destroyErr := sb.Destroy(cleanupCtx)
	live, listErr := s.provider.List(cleanupCtx)
	verified := destroyErr == nil && listErr == nil
	for _, v := range live {
		if v.ID == sb.ID() {
			verified = false
		}
	}
	s.finish(r, ctx, outcome, checkpoint, &initial, failure, verified)
}
func (s *Service) restore(ctx context.Context, sb sandbox.Sandbox, req Request) (Manifest, error) {
	initial := Manifest{Files: []File{}}
	byPath := map[string]File{}
	for _, sha := range []string{req.Manifest, req.Checkpoint} {
		if sha != "" {
			m, err := s.manifest(sha)
			if err != nil {
				return initial, err
			}
			for _, f := range m.Files {
				byPath[f.Path] = f
			}
			if m.HarnessSessionID != "" {
				initial.HarnessSessionID = m.HarnessSessionID
			}
		}
	}
	for _, f := range byPath {
		initial.Files = append(initial.Files, f)
	}
	sort.Slice(initial.Files, func(i, j int) bool { return initial.Files[i].Path < initial.Files[j].Path })
	if err := validateManifest(initial); err != nil {
		return initial, err
	}
	out, err := sb.Execute(ctx, sandbox.Command{Argv: []string{"python3", "-c", "import os; os.makedirs('/workspace/sf',exist_ok=False)"}, Timeout: 30 * time.Second})
	if err != nil {
		return initial, err
	}
	if !out.Succeeded() {
		return initial, errors.New("workspace already exists or unavailable")
	}
	for _, f := range initial.Files {
		if err := sb.WriteFile(ctx, workspace+"/"+f.Path, f.Content); err != nil {
			return initial, err
		}
	}
	return initial, nil
}
func parseHarness(stdout string) map[string]any {
	var result map[string]any
	if json.Unmarshal([]byte(stdout), &result) == nil {
		return result
	}
	for _, line := range strings.Split(stdout, "\n") {
		var v map[string]any
		if json.Unmarshal([]byte(line), &v) == nil && v["type"] == "result" {
			result = v
		}
	}
	if result == nil {
		result = map[string]any{}
	}
	return result
}
func (s *Service) finish(r *record, ctx context.Context, out sandbox.Execution, checkpoint Manifest, initial *Manifest, failure string, verified bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "completed"
	if out.ExitCode != 0 || failure != "" {
		state = "failed"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || out.TimedOut {
		state = "timed_out"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		state = "cancelled"
	}
	// The lease watcher runs once a second. Completion must enforce the exact
	// deadline too, so a fast exit cannot publish success after ownership expires.
	leaseExpired := !time.Now().Before(r.Status.LeaseExpiresAt)
	if leaseExpired && ctx.Err() == nil {
		state = "cancelled"
		failure = "execution ownership lease expired"
	}
	if !verified {
		if failure != "" {
			failure += "; "
		}
		failure += "sandbox cleanup unverified"
		if state == "completed" {
			state = "failed"
		}
	}
	parsed := parseHarness(out.Stdout)
	if parsed["is_error"] == true && state == "completed" {
		state = "failed"
	}
	if (len(parsed) == 0 || (s.profiles[r.Request.Profile].Harness == "claude-code" && parsed["type"] != "result")) && state == "completed" {
		state = "failed"
		failure = "harness produced no terminal result JSON"
	}
	if id, ok := parsed["session_id"].(string); ok && idPattern.MatchString(id) {
		checkpoint.HarnessSessionID = id
	}
	checkpointRef := ""
	if checkpoint.Files != nil && ctx.Err() == nil && !leaseExpired {
		var err error
		checkpointRef, err = s.putManifest(checkpoint)
		if err != nil {
			state = "failed"
			failure = "checkpoint persistence failed"
		}
	}
	if failure == "" {
		failure = r.Status.Error
	}
	r.Status.Permissions = nil
	r.Status.State = state
	r.Status.Error = failure
	r.Status.Checkpoint = checkpointRef
	result := s.failure(r, state, failure, verified)
	result.Checkpoint = checkpointRef
	body := result.Result
	body["exit_code"] = out.ExitCode
	body["stdout_tail"] = tail(out.Stdout)
	body["stderr_tail"] = tail(out.Stderr)
	harness := map[string]any{"name": s.profiles[r.Request.Profile].Harness}
	for _, key := range []string{"session_id", "num_turns", "is_error"} {
		if v, ok := parsed[key]; ok {
			harness[key] = v
		}
	}
	body["harness"] = harness
	if v, ok := parsed["structured_output"]; ok {
		body["output"] = map[string]any{"value": v, "valid": false, "errors": []string{"awaiting sf schema validation"}, "sha256": nil}
	}
	if v, ok := parsed["result"].(string); ok {
		body["result_text"] = v
	}
	usage := map[string]any{}
	if v, ok := parsed["usage"].(map[string]any); ok {
		for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
			if n, ok := v[key].(float64); ok && n >= 0 {
				usage[key] = n
			}
		}
	}
	if cost, ok := parsed["total_cost_usd"].(float64); ok && cost >= 0 {
		backend := body["backend"].(map[string]any)
		backend["harness_reported_cost_usd"] = cost
		tokens := 0.0
		for _, key := range []string{"input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
			if n, ok := usage[key].(float64); ok {
				tokens += n
			}
		}
		if strings.Contains(out.Stderr, "unrecognized_model") || (cost == 0 && tokens > 0) {
			backend["cost_status"] = "unknown_model_pricing"
		} else {
			usage["cost_usd"] = cost
			backend["cost_status"] = "harness_reported"
		}
	}
	if len(usage) > 0 {
		body["usage"] = usage
	}
	signals := body["signals"].(map[string]any)
	signals["cost_usd"] = usage["cost_usd"]
	signals["exit_code"] = out.ExitCode
	before := map[string]string{}
	if initial != nil {
		for _, f := range initial.Files {
			before[f.Path] = digest(f.Content)
		}
	}
	artifacts := []any{}
	for _, f := range checkpoint.Files {
		if !strings.HasPrefix(f.Path, ".home/") && before[f.Path] != digest(f.Content) {
			artifacts = append(artifacts, map[string]any{"source": "workspace", "path": f.Path, "sha256": "sha256:" + digest(f.Content), "bytes": len(f.Content), "modified_at": time.Now().UTC().Format(time.RFC3339Nano)})
		}
	}
	body["artifacts"] = artifacts
	signals["files_written"] = len(artifacts)
	r.Result = result
	s.event(r, "state", map[string]any{"status": state, "cleanup_verified": verified})
	if err := s.persist(r); err != nil {
		r.Status.State = "failed"
		r.Status.Error = "terminal state persistence failed"
		r.Result = nil
	}
}
func tail(s string) string {
	if len(s) > 4000 {
		return s[len(s)-4000:]
	}
	return s
}
func (s *Service) failure(r *record, state, message string, verified bool) *Result {
	now := time.Now().UTC()
	duration := now.Sub(r.StartedAt).Milliseconds()
	residual := []string{}
	if !verified {
		if r.SandboxID != "" {
			residual = append(residual, r.SandboxID)
		} else {
			residual = append(residual, "sandbox creation outcome unknown")
		}
	}
	return &Result{Error: message, Result: map[string]any{"apiVersion": "superfactory/v0", "kind": "TurnResult", "run_id": r.Request.ExecutionID, "spec_sha": r.Request.SpecSHA, "status": state, "exit_code": nil, "started_at": r.StartedAt.Format(time.RFC3339Nano), "finished_at": now.Format(time.RFC3339Nano), "duration_ms": duration, "harness": map[string]any{"name": s.profiles[r.Request.Profile].Harness}, "sandbox": map[string]any{"provider": s.provider.Name(), "network": "profile", "cleanup": map[string]any{"verified": verified, "residual": residual}}, "artifacts": []any{}, "stdout_tail": "", "stderr_tail": "", "signals": map[string]any{"files_written": 0, "deliverables_written": []string{}, "repos_touched": []string{}, "irreversible_calls": []string{}, "egress": nil, "cost_usd": nil, "duration_ms": duration, "exit_code": nil}, "backend": map[string]any{"profile_sha": r.ProfileSHA, "failure": message}}}
}

// A create request can succeed upstream while its response is lost. Reconcile
// by immutable service/execution metadata rather than inventing a sandbox ID.
func (s *Service) cleanupIdentity(executionID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	live, err := s.provider.List(ctx)
	if err != nil {
		return false
	}
	verified := true
	matches := func(info sandbox.Info) bool {
		return info.Metadata["esf.turn.id"] == executionID && info.Metadata["esf.turn.service"] == digest([]byte(s.root))
	}
	for _, info := range live {
		if matches(info) {
			if s.provider.Destroy(ctx, info.ID) != nil {
				verified = false
			}
		}
	}
	remaining, err := s.provider.List(ctx)
	if err != nil {
		return false
	}
	for _, info := range remaining {
		if matches(info) {
			verified = false
		}
	}
	return verified
}
