package turnexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

func TestCompiledPolicyDisablesImplicitMCPServers(t *testing.T) {
	p := profiles()["claude"]
	p.Command = []string{"claude"}
	p.TurnPolicy = &TurnPolicy{}
	argv := policyCommand(p)
	strict := false
	config := ""
	for i, arg := range argv {
		if arg == "--strict-mcp-config" {
			strict = true
		}
		if arg == "--mcp-config" && i+1 < len(argv) {
			config = argv[i+1]
		}
	}
	var servers struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if !strict || json.Unmarshal([]byte(config), &servers) != nil || servers.MCPServers == nil || len(servers.MCPServers) != 0 {
		t.Fatalf("compiled policy can load MCP servers from the restored workspace or home: %q", argv)
	}
}

func TestClaudeRequiresTerminalResult(t *testing.T) {
	for _, stdout := range []string{
		`{"type":"system","subtype":"init","session_id":"session-one"}`,
		`{"type":"assistant","message":{"content":"unfinished"}}`,
		`{"result":"looks successful but is not a terminal result"}`,
	} {
		t.Run(stdout, func(t *testing.T) {
			s, _ := fixture(t)
			r := &record{Status: Status{ExecutionID: "incomplete", State: "running", LeaseExpiresAt: time.Now().Add(time.Minute)}, Request: request("incomplete"), StartedAt: time.Now(), Events: []Event{}}
			s.finish(r, context.Background(), sandbox.Execution{ExitCode: 0, Stdout: stdout}, Manifest{Files: []File{}}, nil, "", true)
			if r.Result.Result["status"] != "failed" || !strings.Contains(r.Result.Error, "terminal result") {
				t.Fatalf("nonterminal harness output reported success: %+v", r.Result)
			}
		})
	}
}

func TestExpiredLeaseCannotPublishCompletedCheckpoint(t *testing.T) {
	s, _ := fixture(t)
	r := &record{Status: Status{ExecutionID: "expired-completion", State: "running", LeaseExpiresAt: time.Now().Add(-time.Millisecond)}, Request: request("expired-completion"), StartedAt: time.Now(), Events: []Event{}}
	// Finish before the periodic lease watcher would have observed expiration.
	s.finish(r, context.Background(), sandbox.Execution{ExitCode: 0, Stdout: `{"type":"result","result":"done"}`}, Manifest{Files: []File{{Path: "out.txt", Content: []byte("late")}}}, nil, "", true)
	if r.Result.Result["status"] != "cancelled" || r.Result.Checkpoint != "" || !strings.Contains(r.Result.Error, "lease expired") {
		t.Fatalf("expired ownership published a result or checkpoint: %+v", r.Result)
	}
}
