package securityassessment

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const AssessmentInputSchema = "esf.fixed-assessment-input/v1"

var FixedAssessmentChecks = []string{"tenant-control", "tenant-isolation", "denial-telemetry", "evidence-integrity"}

type SuppliedLogs struct {
	Bytes          []byte
	Complete       bool
	ExpectedSHA256 string
}

type RunInput struct {
	Schema        string                                              `json:"schema"`
	Profile       Profile                                             `json:"profile"`
	Identity      Identity                                            `json:"identity"`
	Authorization string                                              `json:"authorization"`
	EndpointToken string                                              `json:"endpoint_token"`
	WorkspaceRoot string                                              `json:"workspace_root"`
	ReadLogs      func(context.Context, string) (SuppliedLogs, error) `json:"-"`
}

type RunOutput struct {
	Manifest             Manifest          `json:"manifest"`
	EngagementID         string            `json:"engagement_id,omitempty"`
	ControlCorrelation   string            `json:"control_correlation"`
	IsolationCorrelation string            `json:"isolation_correlation"`
	PrivateEvidence      map[string][]byte `json:"-"`
	SourceLogSHA256      string            `json:"source_log_sha256,omitempty"`
	ErrorCode            string            `json:"error_code,omitempty"`
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func FixedRequestDigest(profile Profile, runID, targetInstanceID, authorization, endpointToken string) (string, error) {
	profileDigest, err := profile.Digest()
	if err != nil {
		return "", err
	}
	if !identifierPattern.MatchString(runID) || !identifierPattern.MatchString(targetInstanceID) || !strings.HasPrefix(authorization, "Bearer ") || len(authorization) < 16 || len(authorization) > 1024 || len(endpointToken) < 16 || len(endpointToken) > 256 || strings.ContainsAny(authorization+endpointToken, "\r\n\x00") {
		return "", errors.New("invalid fixed request identity")
	}
	policy := struct {
		Schema                   string   `json:"schema"`
		ProfileDigest            string   `json:"profile_digest"`
		RunID                    string   `json:"run_id"`
		TargetID                 string   `json:"target_instance_id"`
		TargetURL                string   `json:"target_url"`
		Method                   string   `json:"method"`
		Paths                    []string `json:"paths"`
		Actor                    string   `json:"actor"`
		AuthorizationSHA256      string   `json:"authorization_sha256"`
		EndpointCapabilitySHA256 string   `json:"endpoint_capability_sha256"`
		FollowRedirects          bool     `json:"follow_redirects"`
		TimeoutSeconds           int      `json:"timeout_seconds"`
		CorrelationDerivation    string   `json:"correlation_derivation"`
	}{"esf.fixed-request-policy/v1", profileDigest, runID, targetInstanceID, strings.TrimRight(profile.TargetURL, "/"), "GET", []string{"/api/resources/a", "/api/resources/b"}, "a", SHA256([]byte(authorization)), SHA256([]byte(endpointToken)), false, 10, "esf-sha256-run-request-path/v1"}
	data, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	return SHA256(data), nil
}

func validateRun(input RunInput) error {
	if input.Schema != AssessmentInputSchema || input.ReadLogs == nil || !filepath.IsAbs(input.WorkspaceRoot) || filepath.Clean(input.WorkspaceRoot) == "/" {
		return errors.New("invalid fixed assessment input")
	}
	if err := input.Profile.Validate(); err != nil {
		return err
	}
	if err := input.Identity.Validate(); err != nil {
		return err
	}
	digest, err := input.Profile.Digest()
	if err != nil || digest != input.Identity.ProfileDigest || !sameJSON(input.Profile.Runtime, input.Identity.Runtime) || !sameJSON(input.Profile.Limits, input.Identity.Limits) || !sameJSON(input.Profile.AI, input.Identity.AI) {
		return errors.New("trusted assessment profile identity mismatch")
	}
	actual := append([]string(nil), input.Profile.RequiredChecks...)
	expected := append([]string(nil), FixedAssessmentChecks...)
	sort.Strings(actual)
	sort.Strings(expected)
	identityChecks := append([]string(nil), input.Identity.RequiredChecks...)
	sort.Strings(identityChecks)
	if !sameJSON(actual, expected) || !sameJSON(actual, identityChecks) {
		return errors.New("fixed assessment required-check set mismatch")
	}
	requestDigest, err := FixedRequestDigest(input.Profile, input.Identity.RunID, input.Identity.TargetInstanceID, input.Authorization, input.EndpointToken)
	if err != nil || requestDigest != input.Identity.RequestDigest {
		return errors.New("fixed request identity mismatch")
	}
	for _, tool := range []string{"oc_engagement_create", "oc_http_request", "oc_log_timeline", "oc_evidence_export", "oc_evidence_verify", "oc_audit_verify"} {
		found := false
		for _, name := range input.Profile.RequiredTools {
			if tool == name {
				found = true
			}
		}
		if !found {
			return errors.New("fixed assessment required tool missing")
		}
	}
	return nil
}

func initialManifest(input RunInput) Manifest {
	m := Manifest{Schema: ManifestSchema, Identity: input.Identity, Cleanup: Cleanup{Status: Incomplete}, AI: AIOutcome{Status: "not-requested"}}
	if input.Profile.AI.Enabled {
		m.AI = AIOutcome{Status: "unavailable", Provider: input.Profile.AI.Provider, Model: input.Profile.AI.Model}
	}
	for _, id := range FixedAssessmentChecks {
		m.Coverage = append(m.Coverage, Coverage{ID: id, Status: Incomplete, Code: "not-executed"})
		m.Assertions = append(m.Assertions, Assertion{ID: id, Status: Unobserved})
	}
	return m
}

// RunFixedAssessment executes the synthetic fixed profile. The controller must
// retain its outputs, independently verify identity/hashes, and set cleanup
// only after its owned VMs/routes have actually been reconciled.
func RunFixedAssessment(ctx context.Context, input RunInput, transport ToolTransport) (output RunOutput, returnErr error) {
	output = RunOutput{Manifest: initialManifest(input), PrivateEvidence: map[string][]byte{}}
	if err := validateRun(input); err != nil {
		output.ErrorCode = "invalid-input"
		return output, err
	}
	ctx, cancel := context.WithTimeout(ctx, input.Profile.Limits.MaxDuration)
	defer cancel()
	driver, err := NewAssessmentDriver(input.Profile, input.Identity.RunID, transport)
	if err != nil {
		output.ErrorCode = "invalid-profile"
		return output, err
	}
	correlation := func(suffix string) string {
		return "esf-" + SHA256([]byte(input.Identity.RunID + ":" + input.Identity.RequestDigest + ":" + suffix))[:40]
	}
	output.ControlCorrelation = correlation("control")
	output.IsolationCorrelation = correlation("isolation")
	setCheck := func(id string, coverage CoverageStatus, status AssertionStatus, code, cid string, names ...string) {
		for index := range output.Manifest.Coverage {
			if output.Manifest.Coverage[index].ID == id {
				output.Manifest.Coverage[index] = Coverage{ID: id, Status: coverage, Code: code}
			}
		}
		for index := range output.Manifest.Assertions {
			if output.Manifest.Assertions[index].ID == id {
				output.Manifest.Assertions[index] = Assertion{ID: id, Status: status, CorrelationID: cid, EvidenceNames: names}
			}
		}
	}
	retain := func(name string, raw []byte) error {
		data := bytes.Clone(raw)
		for _, secret := range []string{input.Authorization, strings.TrimPrefix(input.Authorization, "Bearer "), input.EndpointToken} {
			if secret != "" {
				data = bytes.ReplaceAll(data, []byte(secret), []byte("[REDACTED]"))
			}
		}
		var total int64
		for _, current := range output.PrivateEvidence {
			total += int64(len(current))
		}
		if len(data) == 0 || total+int64(len(data)) > input.Profile.Limits.MaxEvidenceBytes {
			return errors.New("selected evidence exceeds assessment limit")
		}
		output.PrivateEvidence[name] = data
		return nil
	}
	defer func() {
		output.Manifest.Evidence = nil
		names := make([]string, 0, len(output.PrivateEvidence))
		for name := range output.PrivateEvidence {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			data := output.PrivateEvidence[name]
			output.Manifest.Evidence = append(output.Manifest.Evidence, EvidenceRef{Name: name, SHA256: SHA256(data), Bytes: int64(len(data))})
		}
	}()
	if _, err := driver.CreateEngagement(ctx); err != nil {
		output.ErrorCode = "engagement-incomplete"
		return output, err
	}
	output.EngagementID = driver.EngagementID()
	rootPath := filepath.Join(input.WorkspaceRoot, "engagements", output.EngagementID)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		output.ErrorCode = "engagement-workspace-unavailable"
		return output, errors.New("owned engagement workspace unavailable")
	}
	defer root.Close()
	write := func(name string, data []byte) error {
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("owned evidence path unavailable")
		}
		_, writeErr := f.Write(data)
		syncErr := f.Sync()
		closeErr := f.Close()
		return errors.Join(writeErr, syncErr, closeErr)
	}
	read := func(name string) ([]byte, error) {
		f, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > input.Profile.Limits.MaxEvidenceBytes {
			return nil, errors.New("evidence archive invalid or oversized")
		}
		data, err := io.ReadAll(io.LimitReader(f, input.Profile.Limits.MaxEvidenceBytes+1))
		if err != nil || int64(len(data)) > input.Profile.Limits.MaxEvidenceBytes {
			return nil, errors.New("evidence archive incomplete")
		}
		return data, nil
	}
	controlURL := strings.TrimRight(input.Profile.TargetURL, "/") + "/api/resources/a"
	control, err := driver.Request(ctx, "/api/resources/a", output.ControlCorrelation, input.Authorization, input.EndpointToken)
	if err != nil {
		output.ErrorCode = "control-incomplete"
		return output, err
	}
	if err := retain("http-control.json", control); err != nil {
		output.ErrorCode = "evidence-limit"
		return output, err
	}
	status := judgeHTTP(control, controlURL, output.ControlCorrelation, "a", false)
	setCheck("tenant-control", Complete, status, "", output.ControlCorrelation, "http-control.json")
	if status != Passed {
		output.ErrorCode = "positive-control-failed"
		return output, errors.New("legitimate access control did not succeed")
	}
	isolationURL := strings.TrimRight(input.Profile.TargetURL, "/") + "/api/resources/b"
	isolation, err := driver.Request(ctx, "/api/resources/b", output.IsolationCorrelation, input.Authorization, input.EndpointToken)
	if err != nil {
		output.ErrorCode = "isolation-incomplete"
		return output, err
	}
	if err := retain("http-isolation.json", isolation); err != nil {
		output.ErrorCode = "evidence-limit"
		return output, err
	}
	status = judgeHTTP(isolation, isolationURL, output.IsolationCorrelation, "b", true)
	coverage := Complete
	if status == Unobserved {
		coverage = Incomplete
	}
	setCheck("tenant-isolation", coverage, status, "", output.IsolationCorrelation, "http-isolation.json")
	logs, err := input.ReadLogs(ctx, output.IsolationCorrelation)
	if err != nil || !logs.Complete || VerifyEvidence(logs.Bytes, logs.ExpectedSHA256) != nil {
		output.ErrorCode = "supplied-logs-incomplete"
		return output, errors.New("supplied application logs incomplete or mismatched")
	}
	output.SourceLogSHA256 = logs.ExpectedSHA256
	normalized, err := normalizeLogs(logs.Bytes, input.Profile.Limits.MaxLogBytes)
	if err != nil {
		output.ErrorCode = "supplied-logs-incomplete"
		return output, err
	}
	if err := write("evidence/application.jsonl", normalized); err != nil {
		output.ErrorCode = "supplied-logs-transfer"
		return output, err
	}
	timeline, err := driver.LogTimeline(ctx)
	if err != nil {
		output.ErrorCode = "log-analysis-incomplete"
		return output, err
	}
	if err := retain("timeline.json", timeline); err != nil {
		output.ErrorCode = "evidence-limit"
		return output, err
	}
	report, reportErr := readTimelineReport(timeline, output.EngagementID, normalized, read)
	if len(report) > 0 {
		if err := retain("timeline-report.json", report); err != nil {
			output.ErrorCode = "evidence-limit"
			return output, err
		}
	}
	if reportErr != nil {
		output.ErrorCode = "log-analysis-report-incomplete"
		return output, reportErr
	}
	telemetry := judgeTelemetry(report, output.IsolationCorrelation)
	if !timelineHasCorrelation(report, output.IsolationCorrelation) {
		output.ErrorCode = "log-analysis-correlation-missing"
		return output, errors.New("analyzed application log lacks the exact isolation request")
	}
	setCheck("denial-telemetry", Complete, telemetry, "", output.IsolationCorrelation, "timeline.json", "timeline-report.json")
	summary, _ := json.Marshal(struct {
		Schema          string      `json:"schema"`
		Identity        Identity    `json:"identity"`
		Assertions      []Assertion `json:"assertions"`
		SourceLogSHA256 string      `json:"source_log_sha256"`
	}{"esf.fixed-assertions/v1", input.Identity, output.Manifest.Assertions, output.SourceLogSHA256})
	if err := write("evidence/assessment.json", summary); err != nil {
		output.ErrorCode = "assessment-evidence-write"
		return output, err
	}
	exported, err := driver.Export(ctx)
	if err != nil {
		output.ErrorCode = "export-incomplete"
		return output, err
	}
	archive, err := read("evidence/esf-export.tar.gz")
	if err != nil || VerifyEvidence(archive, driver.exportHash) != nil {
		output.ErrorCode = "export-hash-mismatch"
		return output, errors.New("selected export differs from independently held transport hash")
	}
	verified, err := driver.VerifyExport(ctx)
	if err != nil {
		output.ErrorCode = "export-verification-incomplete"
		return output, err
	}
	var verification struct {
		Valid bool `json:"valid"`
	}
	if json.Unmarshal(verified, &verification) != nil || !verification.Valid {
		output.ErrorCode = "export-verification-failed"
		return output, errors.New("selected evidence export failed integrity verification")
	}
	audit, err := driver.Audit(ctx)
	if err != nil {
		output.ErrorCode = "audit-incomplete"
		return output, err
	}
	var auditResult struct {
		Valid bool `json:"valid"`
	}
	if json.Unmarshal(audit, &auditResult) != nil || !auditResult.Valid {
		output.ErrorCode = "audit-verification-failed"
		return output, errors.New("assessment audit chain failed")
	}
	// The archive is private binary evidence: preserve its exact bytes and hash.
	var total int64
	for _, data := range output.PrivateEvidence {
		total += int64(len(data))
	}
	if total+int64(len(archive)) > input.Profile.Limits.MaxEvidenceBytes {
		output.ErrorCode = "evidence-limit"
		return output, errors.New("selected evidence exceeds assessment limit")
	}
	output.PrivateEvidence["opencyber-export.tar.gz"] = archive
	if err := retain("export-receipt.json", exported); err != nil {
		output.ErrorCode = "evidence-limit"
		return output, err
	}
	if err := retain("export-verification.json", verified); err != nil {
		output.ErrorCode = "evidence-limit"
		return output, err
	}
	setCheck("evidence-integrity", Complete, Passed, "", "", "opencyber-export.tar.gz", "export-verification.json")
	return output, nil
}

func judgeHTTP(raw []byte, expectedURL, correlation, owner string, isolation bool) AssertionStatus {
	var result struct {
		Status       int               `json:"status"`
		URL          string            `json:"url"`
		RequestedURL string            `json:"requested_url"`
		Headers      map[string]string `json:"headers"`
		Body         string            `json:"body"`
	}
	if json.Unmarshal(raw, &result) != nil || result.URL != expectedURL || result.RequestedURL != expectedURL {
		return Unobserved
	}
	echo, routeResult := "", ""
	for name, value := range result.Headers {
		if strings.EqualFold(name, "X-Correlation-ID") {
			echo = value
		}
		if strings.EqualFold(name, "X-ESF-Route-Result") {
			routeResult = value
		}
	}
	if echo != correlation || routeResult != "forwarded" {
		return Unobserved
	}
	if isolation && (result.Status == 403 || result.Status == 404) {
		return Passed
	}
	if result.Status != 200 {
		return Unobserved
	}
	var body struct {
		ID          string `json:"id"`
		Owner       string `json:"owner"`
		RequestedBy string `json:"requested_by"`
	}
	if json.Unmarshal([]byte(result.Body), &body) != nil || body.ID != owner || body.Owner != owner || body.RequestedBy != "a" {
		return Unobserved
	}
	if isolation {
		return Failed
	}
	return Passed
}

func normalizeLogs(data []byte, maxBytes int64) ([]byte, error) {
	if len(data) == 0 || int64(len(data)) > maxBytes || data[len(data)-1] != '\n' {
		return nil, errors.New("supplied logs missing, oversized or truncated")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var output bytes.Buffer
	count := 0
	for scanner.Scan() {
		count++
		if count > 10000 {
			return nil, errors.New("supplied log record limit exceeded")
		}
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.UseNumber()
		var event map[string]any
		if !utf8.Valid(scanner.Bytes()) || decoder.Decode(&event) != nil || event == nil || decoder.Decode(new(any)) != io.EOF {
			return nil, errors.New("supplied logs contain malformed record")
		}
		if timestamp, ok := event["timestamp"].(string); ok {
			if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
				return nil, errors.New("supplied timestamp is unresolved")
			}
		} else {
			number, ok := event["time_ns"].(json.Number)
			if !ok {
				return nil, errors.New("supplied log timestamp missing")
			}
			ns, err := strconv.ParseInt(string(number), 10, 64)
			if err != nil {
				return nil, errors.New("supplied log timestamp invalid")
			}
			event["timestamp"] = time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
		}
		line, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		output.Write(line)
		output.WriteByte('\n')
		if int64(output.Len()) > maxBytes {
			return nil, errors.New("normalized logs exceed profile bound")
		}
	}
	if scanner.Err() != nil || count == 0 {
		return nil, errors.New("supplied logs incomplete")
	}
	return output.Bytes(), nil
}

type timelineSource struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	HashComplete bool   `json:"hash_complete"`
}

type timelineReport struct {
	Schema             int            `json:"schema_version"`
	Kind               string         `json:"kind"`
	EngagementID       string         `json:"engagement_id"`
	Source             timelineSource `json:"source"`
	Complete           bool           `json:"complete"`
	IncompleteReasons  []string       `json:"incomplete_reasons"`
	Errors             []any          `json:"errors"`
	AnalysisBytes      int64          `json:"analysis_bytes"`
	RecordsEncountered int            `json:"records_encountered"`
	Events             []struct {
		LineNumber   int             `json:"line_number"`
		TimestampUTC string          `json:"timestamp_utc"`
		Record       json.RawMessage `json:"record"`
	} `json:"events"`
}

var timelineReportPath = regexp.MustCompile(`^evidence/triage/log_timeline-[a-f0-9]{32}\.json$`)

// OpenCyber returns a report locator, not inline events. Bind its complete
// report to the exact normalized source bytes and each actual source record.
func readTimelineReport(envelope []byte, engagement string, normalized []byte, read func(string) ([]byte, error)) ([]byte, error) {
	var response struct {
		EngagementID      string         `json:"engagement_id"`
		Source            timelineSource `json:"source"`
		ReportPath        string         `json:"report_path"`
		Complete          bool           `json:"complete"`
		IncompleteReasons []string       `json:"incomplete_reasons"`
		ErrorCount        *int           `json:"error_count"`
		Coverage          struct {
			Status string `json:"status"`
		} `json:"coverage"`
	}
	invalid := errors.New("timeline report incomplete or not bound to copied application logs")
	if json.Unmarshal(envelope, &response) != nil || response.EngagementID != engagement || !timelineReportPath.MatchString(response.ReportPath) || !response.Complete || len(response.IncompleteReasons) > 0 || response.ErrorCount == nil || *response.ErrorCount != 0 || response.Coverage.Status != "complete" {
		return nil, invalid
	}
	expected := timelineSource{Path: "evidence/application.jsonl", Size: int64(len(normalized)), SHA256: SHA256(normalized), HashComplete: true}
	if !sameJSON(response.Source, expected) {
		return nil, invalid
	}
	raw, err := read(response.ReportPath)
	if err != nil {
		return nil, invalid
	}
	var report timelineReport
	if json.Unmarshal(raw, &report) != nil || report.Schema != 1 || report.Kind != "log_timeline" || report.EngagementID != engagement || !sameJSON(report.Source, expected) || !report.Complete || report.IncompleteReasons == nil || len(report.IncompleteReasons) > 0 || report.Errors == nil || len(report.Errors) > 0 || report.AnalysisBytes != int64(len(normalized)) {
		return raw, invalid
	}
	lines := bytes.Split(bytes.TrimSuffix(normalized, []byte("\n")), []byte("\n"))
	if report.RecordsEncountered != len(lines) || len(report.Events) != len(lines) {
		return raw, invalid
	}
	seen := map[int]bool{}
	for _, event := range report.Events {
		if event.LineNumber < 1 || event.LineNumber > len(lines) || seen[event.LineNumber] {
			return raw, invalid
		}
		seen[event.LineNumber] = true
		if _, err := time.Parse(time.RFC3339Nano, event.TimestampUTC); err != nil {
			return raw, invalid
		}
		decode := func(data []byte) (any, error) {
			d := json.NewDecoder(bytes.NewReader(data))
			d.UseNumber()
			var value any
			err := d.Decode(&value)
			return value, err
		}
		sourceRecord, a := decode(lines[event.LineNumber-1])
		actualRecord, b := decode(event.Record)
		if a != nil || b != nil || !sameJSON(sourceRecord, actualRecord) {
			return raw, invalid
		}
	}
	return raw, nil
}

func timelineHasCorrelation(raw []byte, correlation string) bool {
	var report timelineReport
	if json.Unmarshal(raw, &report) != nil {
		return false
	}
	for _, event := range report.Events {
		var record struct {
			Correlation string `json:"correlation_id"`
		}
		if json.Unmarshal(event.Record, &record) == nil && record.Correlation == correlation {
			return true
		}
	}
	return false
}

func judgeTelemetry(raw []byte, correlation string) AssertionStatus {
	var result timelineReport
	if json.Unmarshal(raw, &result) != nil || !result.Complete || len(result.IncompleteReasons) > 0 || len(result.Errors) > 0 {
		return Unobserved
	}
	for _, event := range result.Events {
		var record struct {
			CorrelationID string `json:"correlation_id"`
			Status        int    `json:"status"`
		}
		if json.Unmarshal(event.Record, &record) == nil && record.CorrelationID == correlation && (record.Status == 403 || record.Status == 404) {
			return Passed
		}
	}
	return Unobserved
}
