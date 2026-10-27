package securityassessment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeToolTransport struct {
	calls []string
	call  func(string, map[string]any) (json.RawMessage, error)
}

func (f *fakeToolTransport) Call(_ context.Context, name string, args map[string]any) (json.RawMessage, error) {
	f.calls = append(f.calls, name)
	return f.call(name, args)
}

func driverProfile() Profile {
	p := testProfile()
	p.RequiredTools = []string{"oc_engagement_create", "oc_http_request", "oc_log_timeline", "oc_evidence_export", "oc_evidence_verify", "oc_audit_verify"}
	for _, name := range p.RequiredTools {
		p.Runtime.Tools[name] = "0.5.0"
	}
	return p
}

func toolEnvelope(name string, fields map[string]any) json.RawMessage {
	fields["ok"] = true
	fields["contract"] = map[string]any{"schema_version": 1, "contract_schema_version": 1, "tool": name, "server_version": "0.5.0"}
	fields["coverage"] = map[string]any{"status": "complete"}
	data, _ := json.Marshal(fields)
	return data
}

func createDriver(t *testing.T, transport *fakeToolTransport) *AssessmentDriver {
	t.Helper()
	driver, err := NewAssessmentDriver(driverProfile(), "run-a", transport)
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestDriverRejectsCapabilitiesAndArgumentsBeforeTransport(t *testing.T) {
	fake := &fakeToolTransport{call: func(name string, args map[string]any) (json.RawMessage, error) {
		return toolEnvelope(name, map[string]any{"engagement_id": "eng_20261027_" + strings.Repeat("a", 32)}), nil
	}}
	driver := createDriver(t, fake)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"oc_shell", map[string]any{"command": "id"}},
		{"oc_engagement_create", map[string]any{"name": "other", "targets": []string{"https://unapproved.test"}}},
		{"oc_http_request", map[string]any{"method": "GET", "url": "https://unapproved.test", "engagement_id": "other"}},
	} {
		if _, err := driver.Call(context.Background(), tc.name, tc.args); err == nil {
			t.Fatalf("accepted forbidden invocation %s", tc.name)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("forbidden request crossed capability boundary: %v", fake.calls)
	}
	if _, err := driver.CreateEngagement(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := driver.httpArguments("/api/resources/a", "request-a", "Bearer synthetic-token", "endpoint-capability-token")
	args["follow_redirects"] = true
	if _, err := driver.Call(context.Background(), "oc_http_request", args); err == nil {
		t.Fatal("enabled redirect following")
	}
	args = driver.httpArguments("/api/resources/a", "request-a", "Bearer synthetic-token", "endpoint-capability-token")
	args["engagement_id"] = "eng_20261027_" + strings.Repeat("b", 32)
	if _, err := driver.Call(context.Background(), "oc_http_request", args); err == nil {
		t.Fatal("cross-engagement invocation accepted")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("invalid call reached backend: %v", fake.calls)
	}
}

func TestDriverUnknownAcknowledgementCannotRetryHTTP(t *testing.T) {
	fake := &fakeToolTransport{call: func(name string, args map[string]any) (json.RawMessage, error) {
		if name == "oc_engagement_create" {
			return toolEnvelope(name, map[string]any{"engagement_id": "eng_20261027_" + strings.Repeat("a", 32)}), nil
		}
		return nil, errors.New("unknown acknowledgement")
	}}
	driver := createDriver(t, fake)
	if _, err := driver.CreateEngagement(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := driver.httpArguments("/api/resources/a", "request-a", "Bearer synthetic-token", "endpoint-capability-token")
	if _, err := driver.Call(context.Background(), "oc_http_request", args); err == nil {
		t.Fatal("transport failure hidden")
	}
	if _, err := driver.Call(context.Background(), "oc_http_request", args); err == nil {
		t.Fatal("unsafe retry admitted")
	}
	if len(fake.calls) != 2 {
		t.Fatalf("HTTP was repeated after unknown acknowledgement: %v", fake.calls)
	}
}

func TestDriverRejectsPartialAndMismatchedContracts(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(v map[string]any) { v["coverage"] = map[string]any{"status": "partial"} },
		func(v map[string]any) { v["contract"] = map[string]any{"schema_version": 1, "tool": "oc_shell"} },
		func(v map[string]any) { v["ok"] = false },
		func(v map[string]any) { v["truncated"] = true },
	} {
		fake := &fakeToolTransport{call: func(name string, args map[string]any) (json.RawMessage, error) {
			raw := toolEnvelope(name, map[string]any{"engagement_id": "eng_20261027_" + strings.Repeat("a", 32)})
			var value map[string]any
			json.Unmarshal(raw, &value)
			mutate(value)
			raw, _ = json.Marshal(value)
			return raw, nil
		}}
		if _, err := createDriver(t, fake).CreateEngagement(context.Background()); err == nil {
			t.Fatal("unusable contract output became complete")
		}
	}
}

func TestDriverRequestBudgetAndCancellationPrecedeTransport(t *testing.T) {
	fake := &fakeToolTransport{call: func(name string, args map[string]any) (json.RawMessage, error) {
		return toolEnvelope(name, map[string]any{"engagement_id": "eng_20261027_" + strings.Repeat("a", 32)}), nil
	}}
	profile := driverProfile()
	profile.Limits.MaxRequests = 1
	driver, err := NewAssessmentDriver(profile, "run-a", fake)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.CreateEngagement(context.Background()); err != nil {
		t.Fatal(err)
	}
	args := driver.httpArguments("/api/resources/a", "request-a", "Bearer synthetic-token", "endpoint-capability-token")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := driver.Call(ctx, "oc_http_request", args); err == nil {
		t.Fatal("cancelled request executed")
	}
	if len(fake.calls) != 1 {
		t.Fatal("cancelled operation crossed transport boundary")
	}
	if _, err := driver.Call(context.Background(), "oc_http_request", args); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Call(context.Background(), "oc_http_request", args); err == nil {
		t.Fatal("request limit exceeded")
	}
	if len(fake.calls) != 2 {
		t.Fatal("request budget enforced after execution")
	}
}

func TestStdioMCPRequiresMatchingAcknowledgementAndStructuredToolResult(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		toClient, fromServer := io.Pipe()
		toServer, fromClient := io.Pipe()
		client := NewStdioMCP(toClient, fromClient)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer toServer.Close()
			defer fromServer.Close()
			var request struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			decoder := json.NewDecoder(toServer)
			encoder := json.NewEncoder(fromServer)
			for decoder.Decode(&request) == nil {
				if request.Method == "notifications/initialized" {
					continue
				}
				id := request.ID
				if mismatch {
					id++
				}
				var result any
				switch request.Method {
				case "initialize":
					result = map[string]any{"protocolVersion": "2025-11-25", "serverInfo": map[string]string{"version": "0.5.0"}}
				case "tools/list":
					result = map[string]any{"tools": []any{map[string]string{"name": "oc_http_request"}}}
				case "tools/call":
					result = map[string]any{"structuredContent": json.RawMessage(toolEnvelope("oc_http_request", map[string]any{"status": 403}))}
				default:
					return
				}
				if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}) != nil {
					return
				}
			}
		}()
		err := client.Initialize(ctx, []string{"oc_http_request"})
		if mismatch && err == nil {
			t.Fatal("mismatched acknowledgement accepted")
		}
		if !mismatch {
			if err != nil {
				t.Fatal(err)
			}
			raw, err := client.Call(ctx, "oc_http_request", map[string]any{"method": "GET"})
			if err != nil || !strings.Contains(string(raw), `"status":403`) {
				t.Fatalf("structured MCP result lost: %s %v", raw, err)
			}
		}
		client.Close()
		cancel()
		<-finished
	}
}
