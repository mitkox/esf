package securityassessment

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ToolTransport executes only inside the assessor; the factory supplies its
// fixed driver, immutable profile, and separately enforced endpoint route.
type ToolTransport interface {
	Call(context.Context, string, map[string]any) (json.RawMessage, error)
}

const maxMCPMessage = 4 << 20

type StdioMCP struct {
	reader *bufio.Reader
	input  io.ReadCloser
	output io.WriteCloser
	mu     sync.Mutex
	next   uint64
	failed bool
}

func NewStdioMCP(input io.ReadCloser, output io.WriteCloser) *StdioMCP {
	return &StdioMCP{reader: bufio.NewReader(input), input: input, output: output}
}

func (m *StdioMCP) Close() error { return errors.Join(m.input.Close(), m.output.Close()) }

func (m *StdioMCP) exchange(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failed {
		return nil, errors.New("MCP session outcome unknown; retry refused")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.next++
	request := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", m.next, method, params}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > maxMCPMessage {
		return nil, errors.New("invalid bounded MCP request")
	}
	wire := append(encoded, '\n')
	if written, err := m.output.Write(wire); err != nil || written != len(wire) {
		m.failed = true
		return nil, errors.New("MCP write outcome unknown")
	}
	type answer struct {
		data json.RawMessage
		err  error
	}
	result := make(chan answer, 1)
	go func() {
		for count := 0; count < 32; count++ {
			var line []byte
			for {
				part, err := m.reader.ReadSlice('\n')
				if len(line)+len(part) > maxMCPMessage {
					result <- answer{err: errors.New("MCP result exceeds bound")}
					return
				}
				line = append(line, part...)
				if errors.Is(err, bufio.ErrBufferFull) {
					continue
				}
				if err != nil {
					result <- answer{err: errors.New("MCP acknowledgement missing")}
					return
				}
				break
			}
			var response struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Method  string          `json:"method"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			if json.Unmarshal(line, &response) != nil || response.JSONRPC != "2.0" {
				result <- answer{err: errors.New("malformed MCP response")}
				return
			}
			if response.Method != "" {
				if len(response.ID) > 0 && string(response.ID) != "null" {
					result <- answer{err: errors.New("server-initiated MCP request refused")}
					return
				}
				continue
			}
			if string(response.ID) != strconv.FormatUint(m.next, 10) {
				result <- answer{err: errors.New("MCP acknowledgement identity mismatch")}
				return
			}
			if len(response.Error) > 0 && string(response.Error) != "null" {
				result <- answer{err: errors.New("MCP operation failed")}
				return
			}
			if len(response.Result) == 0 {
				result <- answer{err: errors.New("MCP result missing")}
				return
			}
			result <- answer{data: response.Result}
			return
		}
		result <- answer{err: errors.New("MCP notification bound exceeded")}
	}()
	select {
	case <-ctx.Done():
		m.failed = true
		_ = m.Close()
		return nil, errors.New("MCP request interrupted; acknowledgement unknown")
	case response := <-result:
		if response.err != nil {
			m.failed = true
		}
		return response.data, response.err
	}
}

func (m *StdioMCP) Initialize(ctx context.Context, required []string) error {
	result, err := m.exchange(ctx, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "esf-fixed-assessment", "version": "1"}})
	if err != nil {
		return err
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if json.Unmarshal(result, &initialized) != nil || initialized.ProtocolVersion != "2025-11-25" || initialized.ServerInfo.Version == "" {
		return errors.New("unsupported MCP initialization")
	}
	notification := []byte("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n")
	if _, err := m.output.Write(notification); err != nil {
		m.failed = true
		return errors.New("MCP initialization notification failed")
	}
	result, err = m.exchange(ctx, "tools/list", map[string]any{})
	if err != nil {
		return err
	}
	var tools struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		NextCursor string `json:"nextCursor"`
	}
	if json.Unmarshal(result, &tools) != nil || tools.NextCursor != "" {
		return errors.New("tool availability incomplete")
	}
	available := map[string]bool{}
	for _, tool := range tools.Tools {
		available[tool.Name] = true
	}
	for _, name := range required {
		if !available[name] {
			return errors.New("required assessment tool unavailable")
		}
	}
	return nil
}

func (m *StdioMCP) Call(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	result, err := m.exchange(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var response struct {
		Structured json.RawMessage `json:"structuredContent"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(result, &response) != nil || response.IsError {
		return nil, errors.New("MCP tool result failed")
	}
	if len(response.Structured) > 0 && string(response.Structured) != "null" {
		return response.Structured, nil
	}
	if len(response.Content) == 1 && response.Content[0].Type == "text" && json.Valid([]byte(response.Content[0].Text)) {
		return json.RawMessage(response.Content[0].Text), nil
	}
	return nil, errors.New("MCP structured tool result missing")
}

var engagementPattern = regexp.MustCompile(`^eng_[0-9]{8}_[a-f0-9]{32}$`)

type AssessmentDriver struct {
	profile    Profile
	runID      string
	transport  ToolTransport
	engagement string
	exportHash string
	started    time.Time
	requests   int
	calls      int
	uncertain  bool
}

func NewAssessmentDriver(profile Profile, runID string, transport ToolTransport) (*AssessmentDriver, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(profile.TargetURL)
	if !identifierPattern.MatchString(runID) || transport == nil || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("invalid fixed assessment driver input")
	}
	data, _ := json.Marshal(profile)
	var frozen Profile
	if json.Unmarshal(data, &frozen) != nil {
		return nil, errors.New("cannot freeze assessment policy")
	}
	frozen.TargetURL = strings.TrimRight(frozen.TargetURL, "/")
	return &AssessmentDriver{profile: frozen, runID: runID, transport: transport, started: time.Now()}, nil
}

func (d *AssessmentDriver) EngagementID() string { return d.engagement }

func stringField(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}
func numberField(args map[string]any, key string) float64 {
	value, _ := args[key].(float64)
	return value
}
func exactKeys(args map[string]any, required []string, optional ...string) bool {
	allowed := map[string]bool{}
	for _, key := range append(append([]string(nil), required...), optional...) {
		allowed[key] = true
	}
	for _, key := range required {
		if _, ok := args[key]; !ok {
			return false
		}
	}
	for key := range args {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func (d *AssessmentDriver) validateCall(name string, args map[string]any) error {
	if d.uncertain {
		return errors.New("assessment acknowledgement unknown; no automatic retry")
	}
	if time.Since(d.started) > d.profile.Limits.MaxDuration || d.calls >= d.profile.Limits.MaxRequests+16 {
		return errors.New("assessment attempt limit exhausted")
	}
	if !slices.Contains(d.profile.RequiredTools, name) {
		return errors.New("assessment tool not permitted")
	}
	if name == "oc_engagement_create" {
		targets, ok := args["targets"].([]any)
		if d.engagement != "" || !exactKeys(args, []string{"name", "targets", "objective"}) || stringField(args, "name") != "esf-"+d.runID || stringField(args, "objective") != "fixed-tenant-isolation/v1" || !ok || len(targets) != 1 || targets[0] != d.profile.TargetURL {
			return errors.New("engagement creation outside fixed scope")
		}
		return nil
	}
	if d.engagement == "" || stringField(args, "engagement_id") != d.engagement {
		return errors.New("assessment engagement mismatch")
	}
	switch name {
	case "oc_http_request":
		if !exactKeys(args, []string{"engagement_id", "method", "url", "headers", "follow_redirects", "timeout"}) || stringField(args, "method") != "GET" || args["follow_redirects"] != false || numberField(args, "timeout") <= 0 || numberField(args, "timeout") > 10 || d.requests >= d.profile.Limits.MaxRequests {
			return errors.New("HTTP invocation exceeds fixed profile")
		}
		url := stringField(args, "url")
		if url != d.profile.TargetURL+"/health" && url != d.profile.TargetURL+"/api/resources/a" && url != d.profile.TargetURL+"/api/resources/b" {
			return errors.New("HTTP destination or path outside assessment")
		}
		headers, ok := args["headers"].(map[string]any)
		if !ok || len(headers) > 4 {
			return errors.New("invalid assessment headers")
		}
		seen := map[string]bool{}
		for key, raw := range headers {
			value, ok := raw.(string)
			lower := strings.ToLower(key)
			if !ok || seen[lower] || len(value) > 1024 || strings.ContainsAny(value, "\r\n\x00") {
				return errors.New("invalid assessment header value")
			}
			seen[lower] = true
			switch lower {
			case "authorization":
				if !strings.HasPrefix(value, "Bearer ") || len(value) < 16 {
					return errors.New("invalid synthetic authorization")
				}
			case "x-synthetic-user":
				if value != "a" {
					return errors.New("assessment actor outside profile")
				}
			case "x-correlation-id":
				if !identifierPattern.MatchString(value) {
					return errors.New("invalid request correlation")
				}
			case "x-esf-endpoint-token":
				if len(value) < 16 || len(value) > 256 {
					return errors.New("invalid endpoint capability")
				}
			default:
				return errors.New("HTTP header not permitted")
			}
		}
		if !seen["authorization"] || !seen["x-synthetic-user"] || !seen["x-correlation-id"] {
			return errors.New("assessment request identity missing")
		}
	case "oc_log_timeline":
		if !exactKeys(args, []string{"engagement_id", "path", "format", "timestamp_field", "timezone", "max_bytes", "max_records"}) || stringField(args, "path") != "evidence/application.jsonl" || stringField(args, "format") != "jsonl" || stringField(args, "timestamp_field") != "timestamp" || stringField(args, "timezone") != "UTC" || numberField(args, "max_bytes") <= 0 || numberField(args, "max_bytes") > float64(d.profile.Limits.MaxLogBytes) || numberField(args, "max_records") <= 0 || numberField(args, "max_records") > 10000 {
			return errors.New("log analysis outside supplied profile")
		}
	case "oc_evidence_export":
		paths, ok := args["paths"].([]any)
		if !exactKeys(args, []string{"engagement_id", "paths", "output_path"}) || stringField(args, "output_path") != "evidence/esf-export.tar.gz" || !ok || len(paths) != 2 || paths[0] != "evidence/assessment.json" || paths[1] != "evidence/application.jsonl" {
			return errors.New("evidence export path outside selection")
		}
	case "oc_evidence_verify":
		if !exactKeys(args, []string{"engagement_id", "path", "expected_sha256"}) || stringField(args, "path") != "evidence/esf-export.tar.gz" || d.exportHash == "" || stringField(args, "expected_sha256") != d.exportHash {
			return errors.New("evidence verification identity mismatch")
		}
	case "oc_audit_verify":
		if !exactKeys(args, []string{"engagement_id"}) {
			return errors.New("audit invocation outside engagement")
		}
	default:
		return errors.New("assessment operation unsupported")
	}
	return nil
}

func (d *AssessmentDriver) Call(ctx context.Context, name string, args map[string]any) (json.RawMessage, error) {
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) > 64<<10 {
		return nil, errors.New("invalid assessment arguments")
	}
	var frozen map[string]any
	if json.Unmarshal(encoded, &frozen) != nil {
		return nil, errors.New("invalid assessment arguments")
	}
	if err := d.validateCall(name, frozen); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.calls++
	if name == "oc_http_request" {
		d.requests++
	}
	raw, err := d.transport.Call(ctx, name, frozen)
	if err != nil {
		d.uncertain = true
		return nil, errors.New("assessment operation acknowledgement unknown; retry disabled")
	}
	if len(raw) == 0 || len(raw) > maxMCPMessage {
		return nil, errors.New("assessment result missing or oversized")
	}
	var result struct {
		OK       bool `json:"ok"`
		Contract struct {
			Schema         int    `json:"schema_version"`
			ContractSchema int    `json:"contract_schema_version"`
			Tool           string `json:"tool"`
			Server         string `json:"server_version"`
		} `json:"contract"`
		Coverage struct {
			Status string `json:"status"`
		} `json:"coverage"`
		Truncated    bool   `json:"truncated"`
		Continuation any    `json:"continuation"`
		EngagementID string `json:"engagement_id"`
		SHA256       string `json:"sha256"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.OK || result.Contract.Schema != 1 || result.Contract.ContractSchema != 1 || result.Contract.Tool != name || result.Contract.Server != d.profile.Runtime.Tools[name] || result.Coverage.Status != "complete" || result.Truncated || result.Continuation != nil {
		return nil, errors.New("assessment result coverage or contract incomplete")
	}
	if name == "oc_engagement_create" {
		if !engagementPattern.MatchString(result.EngagementID) {
			return nil, errors.New("invalid backend engagement identity")
		}
		d.engagement = result.EngagementID
	}
	if name == "oc_evidence_export" {
		if !digestPattern.MatchString(result.SHA256) {
			return nil, errors.New("evidence export hash missing")
		}
		d.exportHash = result.SHA256
	}
	return bytes.Clone(raw), nil
}

func (d *AssessmentDriver) CreateEngagement(ctx context.Context) (json.RawMessage, error) {
	return d.Call(ctx, "oc_engagement_create", map[string]any{"name": "esf-" + d.runID, "targets": []string{d.profile.TargetURL}, "objective": "fixed-tenant-isolation/v1"})
}

func (d *AssessmentDriver) httpArguments(path, correlation, authorization, endpointToken string) map[string]any {
	headers := map[string]string{"Authorization": authorization, "X-Synthetic-User": "a", "X-Correlation-ID": correlation}
	if endpointToken != "" {
		headers["X-ESF-Endpoint-Token"] = endpointToken
	}
	return map[string]any{"engagement_id": d.engagement, "method": "GET", "url": d.profile.TargetURL + path, "headers": headers, "follow_redirects": false, "timeout": 10}
}

func (d *AssessmentDriver) Request(ctx context.Context, path, correlation, authorization, endpointToken string) (json.RawMessage, error) {
	return d.Call(ctx, "oc_http_request", d.httpArguments(path, correlation, authorization, endpointToken))
}

func (d *AssessmentDriver) LogTimeline(ctx context.Context) (json.RawMessage, error) {
	return d.Call(ctx, "oc_log_timeline", map[string]any{"engagement_id": d.engagement, "path": "evidence/application.jsonl", "format": "jsonl", "timestamp_field": "timestamp", "timezone": "UTC", "max_bytes": d.profile.Limits.MaxLogBytes, "max_records": 10000})
}

func (d *AssessmentDriver) Export(ctx context.Context) (json.RawMessage, error) {
	return d.Call(ctx, "oc_evidence_export", map[string]any{"engagement_id": d.engagement, "paths": []string{"evidence/assessment.json", "evidence/application.jsonl"}, "output_path": "evidence/esf-export.tar.gz"})
}

func (d *AssessmentDriver) VerifyExport(ctx context.Context) (json.RawMessage, error) {
	return d.Call(ctx, "oc_evidence_verify", map[string]any{"engagement_id": d.engagement, "path": "evidence/esf-export.tar.gz", "expected_sha256": d.exportHash})
}

func (d *AssessmentDriver) Audit(ctx context.Context) (json.RawMessage, error) {
	return d.Call(ctx, "oc_audit_verify", map[string]any{"engagement_id": d.engagement})
}
