package console

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mitkox/esf/internal/factory"
)

const (
	defaultPageLimit = 25
	maxPageLimit     = 100
	maxArtifactBytes = 512 * 1024
	maxPatchBytes    = 2 * 1024 * 1024
	maxTaskBytes     = 32 * 1024
	maxVerification  = 2 * 1024 * 1024
)

func (s *Server) handleHealthz(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "version": s.cfg.Version, "server_time": s.now().UTC()})
}

func (s *Server) handleSession(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, SessionResponse{
		Authenticated:  true,
		Environment:    s.cfg.Environment,
		Version:        s.cfg.Version,
		FactoryVersion: factoryVersion(s.cfg.Factory),
		ServerTime:     s.now().UTC(),
		UptimeSeconds:  int64(s.now().Sub(s.start).Seconds()),
		Operator:       OperatorInfo{Name: "Console reader", Role: "read-only"},
		Capabilities: Capabilities{
			Actions:       false,
			LiveTemporal:  s.cfg.Live,
			LiveSandboxes: s.cfg.Live,
			Search:        true,
			Artifacts:     true,
		},
	})
}

func (s *Server) handleOverview(response http.ResponseWriter, request *http.Request) {
	spec := parseWindow(request.URL.Query().Get("window"), s.now())
	writeJSON(response, http.StatusOK, s.buildOverview(request.Context(), spec))
}

// ── Runs ────────────────────────────────────────────────────────────────────

func (s *Server) handleRuns(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	spec := parseWindow(query.Get("window"), s.now())
	limit := parseInt(query.Get("limit"), defaultPageLimit, maxPageLimit)
	offset := parseOffset(query.Get("cursor"))
	states := splitValues(query["state"])
	harness := capValue(query.Get("harness"))
	repository := capValue(query.Get("repository"))
	scope := capValue(query.Get("scope"))
	changeID := capValue(query.Get("change"))
	search := capQuery(query.Get("query"))

	ordered, _, info := s.snapshot()
	base := make([]*runRecord, 0, len(ordered))
	for _, record := range ordered {
		if !spec.contains(runWindowTime(record)) {
			continue
		}
		if search != "" && !runMatchesQuery(record, search) {
			continue
		}
		base = append(base, record)
	}
	facets := runFacets(base)

	filtered := make([]*runRecord, 0, len(base))
	for _, record := range base {
		if len(states) > 0 && !containsValue(states, string(record.Manifest.FactoryResult)) {
			continue
		}
		if harness != "" && record.Manifest.AgentHarness != harness {
			continue
		}
		if repository != "" && record.Manifest.Repository != repository {
			continue
		}
		if scope != "" && record.Manifest.Scope != scope {
			continue
		}
		if changeID != "" && record.Manifest.ChangeID != changeID {
			continue
		}
		filtered = append(filtered, record)
	}
	sortRuns(filtered, query.Get("sort"))
	page, meta := paginate(filtered, offset, limit)
	summaries := make([]RunSummary, 0, len(page))
	for _, record := range page {
		summaries = append(summaries, summarizeRun(record.ID, record.Manifest))
	}
	writeJSON(response, http.StatusOK, RunsResponse{
		Runs: summaries, Page: meta, Facets: facets, Window: spec.info(),
		GeneratedAt: s.now().UTC(), Index: info,
	})
}

func runFacets(records []*runRecord) Facets {
	states := map[string]int{}
	harnesses := map[string]int{}
	repositories := map[string]int{}
	scopes := map[string]int{}
	for _, record := range records {
		state := string(record.Manifest.FactoryResult)
		if state != "" {
			states[state]++
		}
		if record.Manifest.AgentHarness != "" {
			harnesses[record.Manifest.AgentHarness]++
		}
		if record.Manifest.Repository != "" {
			repositories[record.Manifest.Repository]++
		}
		if record.Manifest.Scope != "" {
			scopes[record.Manifest.Scope]++
		}
	}
	return Facets{
		States:       facetCounts(states),
		Harnesses:    facetCounts(harnesses),
		Repositories: facetCounts(repositories),
		Scopes:       facetCounts(scopes),
	}
}

func (s *Server) handleRunDetail(response http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("id")
	if !validEvidenceID(runID) {
		writeError(response, http.StatusNotFound, "not_found", "Run ID is not valid.")
		return
	}
	_, _, _ = s.snapshot()
	record, ok := s.index.run(runID)
	if !ok {
		// The run may be executing right now: its manifest is only written at
		// the end. Fall back to the workflow's own status instead of a 404.
		s.writeLiveRunDetail(response, request, runID)
		return
	}
	writeJSON(response, http.StatusOK, s.buildRunDetail(request, record))
}

func (s *Server) writeLiveRunDetail(response http.ResponseWriter, request *http.Request, runID string) {
	status, err := s.live.runStatus(request.Context(), runID)
	if err != nil {
		writeError(response, http.StatusNotFound, "not_found", "No evidence or live workflow matches that run ID.")
		return
	}
	summary := RunSummary{
		RunID:              runID,
		ChangeID:           status.ChangeID,
		Scope:              status.Scope,
		FactoryResult:      string(status.State),
		StartedAt:          timePtr(status.StartedAt.UTC()),
		DurationMS:         millis(s.now().Sub(status.StartedAt)),
		SandboxID:          status.SandboxID,
		AgentResult:        string(status.AgentOutcome),
		VerificationResult: string(status.VerifyOutcome),
	}
	if live := s.live.openRuns(request.Context()); live.Available {
		for _, run := range live.Runs {
			if run.RunID != runID {
				continue
			}
			summary.Repository = run.Repository
			summary.AgentHarness = run.Harness
			summary.Scope = firstNonEmpty(run.Scope, summary.Scope)
			summary.ChangeID = firstNonEmpty(run.ChangeID, summary.ChangeID)
			summary.SandboxID = firstNonEmpty(run.SandboxID, summary.SandboxID)
			break
		}
	}
	detail := RunDetail{
		Run:          summary,
		Manifest:     nil,
		Verification: VerificationDetail{Available: false},
		Patch:        PatchDetail{Available: false},
		Artifacts:    []ArtifactInfo{},
		Audit:        []factory.AuditEntry{},
		RelatedRuns:  []RunSummary{},
		Actions:      runActions(runID),
		Warnings:     []string{"No durable manifest exists yet: this is live orchestration state queried from Temporal."},
		Conditions:   status.Conditions,
		Live: &LiveRun{
			RunID: runID, WorkflowID: factory.WorkflowIDForRun(runID),
			State: string(status.State), CurrentStep: status.CurrentStep,
			ChangeID: status.ChangeID, SandboxID: status.SandboxID,
			SandboxDestroyed: status.SandboxGone, Conditions: status.Conditions,
			StartedAt: status.StartedAt.UTC(), Repository: summary.Repository, Harness: summary.AgentHarness,
		},
		GeneratedAt: s.now().UTC(),
	}
	// A run paused at the human-review gate has already written its task,
	// verification and patch evidence; the final manifest only appears after
	// the decision. Expose what exists now so reviewers can inspect it.
	runDir := filepath.Join(s.root, "runs", runID)
	if task, absent := readTaskDetail(runDir); !absent {
		detail.Task = task
	}
	if verification, absent := readVerificationDetail(runDir); !absent {
		detail.Verification = verification
	} else if status.VerifyOutcome != "" {
		detail.Warnings = append(detail.Warnings, "verification/result.json is not available yet for this live run.")
	}
	detail.Patch = readPatchDetail(runDir, factory.ArtifactPatch)
	detail.Artifacts = listArtifacts(runDir)
	detail.Audit = readAuditEntries(s.root, runID)
	writeJSON(response, http.StatusOK, detail)
}

// runEvidenceAvailable reports whether a run has durable evidence or is a live
// workflow. It lets the patch/artifact endpoints serve evidence a paused run
// has already written, before its final manifest exists.
func (s *Server) runEvidenceAvailable(ctx context.Context, runID string) bool {
	if _, ok := s.index.run(runID); ok {
		return true
	}
	_, err := s.live.runStatus(ctx, runID)
	return err == nil
}

func (s *Server) buildRunDetail(request *http.Request, record *runRecord) RunDetail {
	manifest := record.Manifest
	detail := RunDetail{
		Run:          summarizeRun(record.ID, manifest),
		Manifest:     &manifest,
		Verification: VerificationDetail{Available: false},
		Patch:        PatchDetail{Available: false},
		Artifacts:    []ArtifactInfo{},
		Audit:        []factory.AuditEntry{},
		RelatedRuns:  []RunSummary{},
		Actions:      runActions(record.ID),
		Warnings:     []string{},
		Conditions:   append([]factory.Condition(nil), manifest.Conditions...),
		Previews:     manifest.Previews,
		Budget:       manifest.BudgetExceeded,
		GeneratedAt:  s.now().UTC(),
	}
	runDir := filepath.Join(s.root, "runs", record.ID)

	if task, absent := readTaskDetail(runDir); absent {
		detail.Warnings = append(detail.Warnings, "task.json is missing from this run's evidence.")
	} else {
		detail.Task = task
	}
	if verification, absent := readVerificationDetail(runDir); absent {
		if manifest.VerificationResult != "" {
			detail.Warnings = append(detail.Warnings, "verification/result.json is missing from this run's evidence.")
		}
	} else {
		detail.Verification = verification
	}
	detail.Patch = readPatchDetail(runDir, manifest.Patch)
	detail.Artifacts = listArtifacts(runDir)
	detail.Audit = readAuditEntries(s.root, record.ID)
	detail.Security = buildRunSecurity(&manifest)

	if manifest.ChangeID != "" {
		if changeRecord, ok := s.index.change(manifest.ChangeID); ok {
			summary := summarizeChange(changeRecord.Change)
			summary.HasEvidence = true
			detail.Change = &summary
		}
	}
	related := s.index.runsForChange(manifest.ChangeID)
	included := map[string]bool{record.ID: true}
	for _, candidate := range related {
		if included[candidate.ID] || len(detail.RelatedRuns) >= 10 {
			continue
		}
		included[candidate.ID] = true
		detail.RelatedRuns = append(detail.RelatedRuns, summarizeRun(candidate.ID, candidate.Manifest))
	}
	if manifest.ParentRunID != "" && !included[manifest.ParentRunID] {
		if parent, ok := s.index.run(manifest.ParentRunID); ok {
			detail.RelatedRuns = append(detail.RelatedRuns, summarizeRun(parent.ID, parent.Manifest))
		}
	}
	if manifest.Hardening == nil {
		// The run summary reports Measured=false, so the UI can say "not
		// measured" instead of implying a clean scan.
		detail.Warnings = append(detail.Warnings, "No hardening evidence is recorded for this run.")
	}
	_ = request
	return detail
}

var diffPathPattern = regexp.MustCompile(`^diff --git a/(.+?) b/(.+)$`)

// ── Evidence readers ────────────────────────────────────────────────────────

type taskFile struct {
	Task                string    `json:"task"`
	TaskHash            string    `json:"task_hash"`
	AgentHarness        string    `json:"agent_harness"`
	AgentModel          string    `json:"agent_model"`
	Model               string    `json:"model"`
	EgressPolicy        string    `json:"egress_policy"`
	Scope               string    `json:"scope"`
	VerificationProfile string    `json:"verification_profile"`
	Workspace           string    `json:"workspace"`
	RecordedAt          time.Time `json:"recorded_at"`
}

func readTaskDetail(runDir string) (*TaskDetail, bool) {
	data, truncated, err := readFileLimited(filepath.Join(runDir, "task.json"), 256*1024)
	if err != nil {
		return nil, true
	}
	var file taskFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, true
	}
	task, wasTruncated := truncateText(file.Task, maxTaskBytes)
	return &TaskDetail{
		Task: task, Truncated: truncated || wasTruncated, TaskHash: file.TaskHash,
		AgentHarness: file.AgentHarness, AgentModel: file.AgentModel, Model: file.Model,
		EgressPolicy: file.EgressPolicy, Scope: file.Scope,
		VerificationProfile: file.VerificationProfile, Workspace: file.Workspace,
		RecordedAt: timePtr(file.RecordedAt),
	}, false
}

type verificationFile struct {
	Passed      bool      `json:"passed"`
	Profile     string    `json:"profile"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Duration    int64     `json:"duration"`
	Steps       []struct {
		ID             string    `json:"id"`
		Description    string    `json:"description"`
		Argv           []string  `json:"argv"`
		ExitCode       int       `json:"exit_code"`
		Passed         bool      `json:"passed"`
		Mandatory      bool      `json:"mandatory"`
		TimedOut       bool      `json:"timed_out"`
		Duration       int64     `json:"duration"`
		StartedAt      time.Time `json:"started_at"`
		CompletedAt    time.Time `json:"completed_at"`
		StdoutArtifact string    `json:"stdout_artifact"`
		StderrArtifact string    `json:"stderr_artifact"`
	} `json:"steps"`
}

func readVerificationDetail(runDir string) (VerificationDetail, bool) {
	data, _, err := readFileLimited(filepath.Join(runDir, "verification", "result.json"), maxVerification)
	if err != nil {
		return VerificationDetail{Available: false}, true
	}
	var file verificationFile
	if err := json.Unmarshal(data, &file); err != nil {
		return VerificationDetail{Available: false}, true
	}
	detail := VerificationDetail{
		Available: true, Passed: file.Passed, Profile: file.Profile,
		DurationMS: file.Duration / int64(time.Millisecond),
		StartedAt:  file.StartedAt.UTC(), CompletedAt: file.CompletedAt.UTC(),
		Steps: make([]VerificationStep, 0, len(file.Steps)),
	}
	for _, step := range file.Steps {
		detail.Steps = append(detail.Steps, VerificationStep{
			ID: step.ID, Description: step.Description, Argv: step.Argv,
			ExitCode: step.ExitCode, Passed: step.Passed, Mandatory: step.Mandatory,
			TimedOut: step.TimedOut, DurationMS: step.Duration / int64(time.Millisecond),
			StartedAt: step.StartedAt.UTC(), CompletedAt: step.CompletedAt.UTC(),
			StdoutArtifact: step.StdoutArtifact, StderrArtifact: step.StderrArtifact,
		})
	}
	return detail, false
}

func readPatchDetail(runDir, patchPath string) PatchDetail {
	detail := PatchDetail{Available: false}
	// The manifest is evidence, not a trusted host path. Only the factory-owned
	// artifact name is ever read here; the dedicated patch endpoint additionally
	// confines the open with os.OpenRoot.
	if strings.TrimSpace(patchPath) != factory.ArtifactPatch {
		patchPath = factory.ArtifactPatch
	}
	path := filepath.Join(runDir, filepath.FromSlash(patchPath))
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return detail
	}
	detail.Available = true
	detail.Path = patchPath
	detail.Bytes = info.Size()
	data, truncated, err := readFileLimited(path, 512*1024)
	if err != nil {
		return detail
	}
	detail.Truncated = truncated
	stats := parsePatchStats(string(data))
	detail.Files = stats.Files
	detail.Additions = stats.Additions
	detail.Deletions = stats.Deletions
	detail.Binary = stats.Binary
	return detail
}

type patchStats struct {
	Files     []PatchFile
	Additions int
	Deletions int
	Binary    bool
}

func parsePatchStats(text string) patchStats {
	stats := patchStats{Files: []PatchFile{}}
	seen := map[string]int{}
	current := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			path := strings.TrimPrefix(line, "diff --git ")
			if match := diffPathPattern.FindStringSubmatch(line); match != nil {
				path = match[2]
			}
			current = path
			if _, ok := seen[path]; !ok {
				seen[path] = len(stats.Files)
				stats.Files = append(stats.Files, PatchFile{Path: path})
			}
			continue
		}
		if strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch") {
			stats.Binary = true
			continue
		}
		index, ok := seen[current]
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			stats.Additions++
			stats.Files[index].Additions++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			stats.Deletions++
			stats.Files[index].Deletions++
		}
	}
	if len(stats.Files) > 200 {
		stats.Files = stats.Files[:200]
	}
	return stats
}

func listArtifacts(runDir string) []ArtifactInfo {
	list := []ArtifactInfo{}
	count := 0
	_ = fs.WalkDir(os.DirFS(runDir), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if count >= 300 {
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		relative := filepath.ToSlash(path)
		list = append(list, ArtifactInfo{
			Path:        relative,
			Bytes:       info.Size(),
			Kind:        artifactKind(relative),
			Previewable: allowedArtifactPath(relative),
			Truncated:   info.Size() > maxArtifactBytes,
		})
		count++
		return nil
	})
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	return list
}

func artifactKind(path string) string {
	switch {
	case strings.HasSuffix(path, ".patch") || path == factory.ArtifactPatch:
		return "patch"
	case strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".jsonl"):
		return "json"
	case strings.HasSuffix(path, ".log"):
		return "log"
	case strings.HasSuffix(path, ".txt") || strings.HasSuffix(path, ".stdout") || strings.HasSuffix(path, ".stderr"):
		return "text"
	default:
		return "other"
	}
}

var allowedArtifactExact = map[string]bool{
	factory.ArtifactTask:               true,
	factory.ArtifactRun:                true,
	factory.ArtifactEnvironment:        true,
	factory.ArtifactInventory:          true,
	factory.ArtifactBaseline:           true,
	factory.ArtifactCleanup:            true,
	factory.ArtifactGitBefore:          true,
	factory.ArtifactGitAfter:           true,
	factory.ArtifactAgentResult:        true,
	factory.ArtifactAgentPrompt:        true,
	factory.ArtifactAgentStdout:        true,
	factory.ArtifactAgentStderr:        true,
	factory.ArtifactVerificationResult: true,
	factory.ArtifactEgressProbe:        true,
	factory.ArtifactBehavior:           true,
	factory.ArtifactPatchScan:          true,
	factory.ArtifactGateIntegrity:      true,
	factory.ArtifactAudit:              true,
}

var allowedArtifactPrefixes = []string{"verification/", "hardening/"}

func allowedArtifactPath(path string) bool {
	if allowedArtifactExact[path] {
		return true
	}
	for _, prefix := range allowedArtifactPrefixes {
		if strings.HasPrefix(path, prefix) && !strings.Contains(path, "..") && len(path) < 200 {
			return true
		}
	}
	return false
}

func readAuditEntries(root, runID string) []factory.AuditEntry {
	// Read the JSONL directly instead of going through the artifact store: the
	// store constructor creates directories, and a console read must never
	// write to the evidence volume.
	data, err := os.ReadFile(filepath.Join(root, "runs", runID, filepath.FromSlash(factory.ArtifactAudit)))
	if err != nil {
		return []factory.AuditEntry{}
	}
	entries := []factory.AuditEntry{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var entry factory.AuditEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		entries = append(entries, entry)
	}
	return entries
}

func buildRunSecurity(manifest *factory.RunManifest) *RunSecurityDetail {
	hardening := manifest.Hardening
	if hardening == nil {
		return nil
	}
	return &RunSecurityDetail{
		EgressProbe:   hardening.EgressProbe,
		Behavior:      hardening.Behavior,
		BehaviorError: hardening.BehaviorError,
		GateIntegrity: hardening.GateIntegrity,
		Alerts:        hardening.Alerts,
	}
}

func readFileLimited(path string, limit int64) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

// ── Patch and artifacts ─────────────────────────────────────────────────────

func (s *Server) handleRunPatch(response http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("id")
	if !validEvidenceID(runID) {
		writeError(response, http.StatusNotFound, "not_found", "Run ID is not valid.")
		return
	}
	_, _, _ = s.snapshot()
	if !s.runEvidenceAvailable(request.Context(), runID) {
		writeError(response, http.StatusNotFound, "not_found", "Run evidence is unavailable.")
		return
	}
	// The manifest is evidence, never a trusted host path. The patch endpoint
	// serves exactly the factory-owned patch artifact.
	patchPath := factory.ArtifactPatch
	runDir := filepath.Join(s.root, "runs", runID)
	root, err := os.OpenRoot(runDir)
	if err != nil {
		writeError(response, http.StatusNotFound, "not_found", "Run evidence directory is unavailable.")
		return
	}
	defer root.Close()
	file, err := root.Open(filepath.FromSlash(patchPath))
	if err != nil {
		writeError(response, http.StatusNotFound, "not_found", "No patch is recorded for this run.")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxPatchBytes+1))
	if err != nil {
		writeError(response, http.StatusInternalServerError, "read_failed", "The patch could not be read.")
		return
	}
	truncated := false
	if len(data) > maxPatchBytes {
		data = data[:maxPatchBytes]
		truncated = true
	}
	if truncated {
		if request.URL.Query().Get("download") == "true" {
			writeError(response, http.StatusRequestEntityTooLarge, "patch_too_large",
				"The patch exceeds the console preview limit and cannot be downloaded through the console; retrieve the artifact from the factory host.")
			return
		}
		response.Header().Set("X-Console-Truncated", "true")
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Disposition", "inline; filename=\""+runID+".patch\"")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(data)
}

func (s *Server) handleRunArtifact(response http.ResponseWriter, request *http.Request) {
	runID := request.PathValue("id")
	if !validEvidenceID(runID) {
		writeError(response, http.StatusNotFound, "not_found", "Run ID is not valid.")
		return
	}
	path := strings.TrimSpace(request.URL.Query().Get("path"))
	if path == "" {
		writeError(response, http.StatusBadRequest, "invalid_path", "An artifact path is required.")
		return
	}
	if !allowedArtifactPath(path) {
		writeError(response, http.StatusBadRequest, "not_previewable", "That evidence file is not available for preview.")
		return
	}
	_, _, _ = s.snapshot()
	if !s.runEvidenceAvailable(request.Context(), runID) {
		writeError(response, http.StatusNotFound, "not_found", "Run evidence is unavailable.")
		return
	}
	runDir := filepath.Join(s.root, "runs", runID)
	root, err := os.OpenRoot(runDir)
	if err != nil {
		writeError(response, http.StatusNotFound, "not_found", "Run evidence directory is unavailable.")
		return
	}
	defer root.Close()
	file, err := root.Open(filepath.FromSlash(path))
	if err != nil {
		writeError(response, http.StatusNotFound, "not_found", "That evidence file does not exist.")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(response, http.StatusNotFound, "not_found", "That evidence file is not a regular file.")
		return
	}
	content, truncated, err := readArtifactContent(file, info.Size())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "read_failed", "The evidence file could not be read.")
		return
	}
	if !utf8.Valid(content) {
		writeError(response, http.StatusUnsupportedMediaType, "binary", "That evidence file is binary and cannot be previewed.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"path": path, "bytes": info.Size(), "truncated": truncated,
		"kind": artifactKind(path), "content": string(content),
	})
}

func readArtifactContent(file *os.File, size int64) ([]byte, bool, error) {
	if size > maxArtifactBytes {
		// Logs are most useful from the tail; keep the bounded tail rather
		// than the first page of a very long stream.
		if _, err := file.Seek(-maxArtifactBytes, io.SeekEnd); err != nil {
			return nil, false, err
		}
		data, err := io.ReadAll(io.LimitReader(file, maxArtifactBytes))
		if err != nil {
			return nil, false, err
		}
		// The tail can start inside a multibyte rune. Skip continuation bytes
		// so a valid UTF-8 log is not rejected as binary.
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			data = data[1:]
		}
		return data, true, nil
	}
	data, err := io.ReadAll(io.LimitReader(file, maxArtifactBytes+1))
	return data, false, err
}

// ── Changes ─────────────────────────────────────────────────────────────────

func (s *Server) handleChanges(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	spec := parseWindow(query.Get("window"), s.now())
	limit := parseInt(query.Get("limit"), defaultPageLimit, maxPageLimit)
	offset := parseOffset(query.Get("cursor"))
	statuses := splitValues(query["status"])
	repository := strings.TrimSpace(query.Get("repository"))
	scope := strings.TrimSpace(query.Get("scope"))
	search := capQuery(query.Get("query"))

	_, ordered, _ := s.snapshot()
	base := make([]*changeRecord, 0, len(ordered))
	for _, record := range ordered {
		if !spec.contains(record.Change.UpdatedAt) {
			continue
		}
		if search != "" && !changeMatchesQuery(record, search) {
			continue
		}
		base = append(base, record)
	}
	statusCounts := map[string]int{}
	repositoryCounts := map[string]int{}
	scopeCounts := map[string]int{}
	for _, record := range base {
		statusCounts[string(record.Change.Status)]++
		if record.Change.Repository != "" {
			repositoryCounts[record.Change.Repository]++
		}
		if record.Change.Scope != "" {
			scopeCounts[record.Change.Scope]++
		}
	}
	facets := Facets{
		Statuses:     facetCounts(statusCounts),
		Repositories: facetCounts(repositoryCounts),
		Scopes:       facetCounts(scopeCounts),
	}

	filtered := make([]*changeRecord, 0, len(base))
	for _, record := range base {
		if len(statuses) > 0 && !containsValue(statuses, string(record.Change.Status)) {
			continue
		}
		if repository != "" && record.Change.Repository != repository {
			continue
		}
		if scope != "" && record.Change.Scope != scope {
			continue
		}
		filtered = append(filtered, record)
	}
	sortChanges(filtered, query.Get("sort"))
	page, meta := paginate(filtered, offset, limit)
	summaries := make([]ChangeSummary, 0, len(page))
	for _, record := range page {
		summary := summarizeChange(record.Change)
		_, hasRun := s.index.run(record.Change.LastRunID)
		summary.HasEvidence = hasRun
		summaries = append(summaries, summary)
	}
	writeJSON(response, http.StatusOK, ChangesResponse{
		Changes: summaries, Page: meta, Facets: facets, Window: spec.info(), GeneratedAt: s.now().UTC(),
	})
}

func (s *Server) handleChangeDetail(response http.ResponseWriter, request *http.Request) {
	changeID := request.PathValue("id")
	if !validEvidenceID(changeID) {
		writeError(response, http.StatusNotFound, "not_found", "Change ID is not valid.")
		return
	}
	_, _, _ = s.snapshot()
	record, ok := s.index.change(changeID)
	if !ok {
		writeError(response, http.StatusNotFound, "not_found", "No change record matches that ID.")
		return
	}
	change := record.Change
	summary := summarizeChange(change)
	runs := s.index.runsForChange(changeID)
	known := map[string]bool{}
	for _, run := range runs {
		known[run.ID] = true
	}
	for _, activation := range change.Activations {
		if known[activation.RunID] {
			continue
		}
		if run, ok := s.index.run(activation.RunID); ok {
			runs = append(runs, run)
			known[activation.RunID] = true
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runSortTime(runs[i]).After(runSortTime(runs[j])) })
	summaries := make([]RunSummary, 0, len(runs))
	for _, run := range runs {
		summaries = append(summaries, summarizeRun(run.ID, run.Manifest))
	}
	summary.HasEvidence = len(summaries) > 0

	livePaused := false
	if live := s.live.openRuns(request.Context()); live.Available {
		for _, run := range live.Runs {
			if run.RunID == change.LastRunID && run.State == string(factory.StatePaused) {
				livePaused = true
				break
			}
		}
	}
	review := ReviewContext{
		GlobalEnabled: s.cfg.Factory.Review.Enabled,
		Pending:       change.Status == factory.ChangeInReview,
		Actionable:    change.Status == factory.ChangeInReview && livePaused,
	}
	if review.Actionable {
		review.ApproveCommand = "factory review " + shellArg(change.LastRunID) + " --approve"
		review.RejectCommand = "factory review " + shellArg(change.LastRunID) + " --reject --note \"…\""
	} else if review.Pending {
		review.Note = "This is a durable post-run review record: its activation has finished and no live workflow is paused, so there is no review gate to signal. Inspect the evidence here and manage the change through the factory CLI resources."
	}
	// Effective configuration applies request > scope > global precedence.
	configured := review.GlobalEnabled
	if scopeReview, ok := s.cfg.Factory.Scopes[change.Scope]; ok && scopeReview.Review != nil {
		configured = *scopeReview.Review
	}
	review.Configured = configured
	if len(runs) > 0 {
		// The latest manifest records the gate that actually applied (including
		// request-level overrides), so it wins over current config inference.
		latest := runs[0].Manifest
		review.Configured = latest.ReviewConfigured
		review.LatestOutcome = latest.ReviewOutcome
	}
	warnings := []string{}
	if len(summaries) == 0 {
		warnings = append(warnings, "No run evidence is available for this change; activations may have been pruned.")
	}
	writeJSON(response, http.StatusOK, ChangeDetail{
		Change: summary, Runs: summaries, Review: review,
		GeneratedAt: s.now().UTC(), Warnings: warnings,
	})
}

// ── Reviews ─────────────────────────────────────────────────────────────────

func (s *Server) handleReviews(response http.ResponseWriter, request *http.Request) {
	limit := parseInt(request.URL.Query().Get("limit"), 25, maxPageLimit)
	offset := parseOffset(request.URL.Query().Get("cursor"))
	_, changes, _ := s.snapshot()
	live := s.live.openRuns(request.Context())
	now := s.now()

	livePaused := map[string]LiveRun{}
	for _, run := range live.Runs {
		if run.State == string(factory.StatePaused) {
			livePaused[run.RunID] = run
		}
	}

	items := []ReviewItem{}
	pendingReview := 0
	for _, record := range changes {
		change := record.Change
		if change.Status != factory.ChangeInReview {
			continue
		}
		pendingReview++
		item := ReviewItem{
			Kind:         "in_review",
			WaitingSince: change.UpdatedAt.UTC(),
			WaitingMS:    millis(now.Sub(change.UpdatedAt)),
		}
		if paused, ok := livePaused[change.LastRunID]; ok && paused.RunID != "" {
			item.Actionable = true
			item.ApproveCommand = "factory review " + shellArg(change.LastRunID) + " --approve"
			item.RejectCommand = "factory review " + shellArg(change.LastRunID) + " --reject --note \"…\""
		} else {
			item.Note = "Durable post-run review record: the activation completed and no live workflow is paused, so there is no gate to signal."
		}
		summary := summarizeChange(change)
		item.Change = &summary
		runID := change.LastRunID
		if runID == "" && len(change.Activations) > 0 {
			runID = change.Activations[len(change.Activations)-1].RunID
		}
		if run, ok := s.index.run(runID); ok {
			runSummary := summarizeRun(run.ID, run.Manifest)
			item.Run = &runSummary
			item.Previews = run.Manifest.Previews
		}
		if liveRun, ok := livePaused[runID]; ok {
			item.Kind = "paused_run"
			item.Live = &liveRun
			item.WaitingSince = liveRun.StartedAt
			item.WaitingMS = millis(now.Sub(liveRun.StartedAt))
			delete(livePaused, runID)
		}
		items = append(items, item)
	}
	for _, run := range livePaused {
		liveRun := run
		summary := RunSummary{
			RunID: run.RunID, ChangeID: run.ChangeID, Scope: run.Scope,
			Repository: run.Repository, AgentHarness: run.Harness,
			FactoryResult: string(factory.StatePaused), StartedAt: timePtr(run.StartedAt.UTC()),
			DurationMS: millis(now.Sub(run.StartedAt)), SandboxID: run.SandboxID,
		}
		items = append(items, ReviewItem{
			Kind: "paused_run", Run: &summary, Live: &liveRun, Actionable: true,
			WaitingSince: run.StartedAt, WaitingMS: millis(now.Sub(run.StartedAt)),
			ApproveCommand: "factory review " + shellArg(run.RunID) + " --approve",
			RejectCommand:  "factory review " + shellArg(run.RunID) + " --reject --note \"…\"",
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].WaitingSince.Before(items[j].WaitingSince) })
	pausedRuns := 0
	for _, item := range items {
		if item.Kind == "paused_run" {
			pausedRuns++
		}
	}
	pageItems, meta := paginate(items, offset, limit)
	writeJSON(response, http.StatusOK, ReviewsResponse{
		Items: pageItems, Page: meta, Total: len(items), LiveAvailable: live.Available, LiveError: live.Error,
		GeneratedAt: now.UTC(), PendingReview: pendingReview, PausedRuns: pausedRuns,
	})
}

// ── Security / operations / catalog ─────────────────────────────────────────

func (s *Server) handleSecurity(response http.ResponseWriter, request *http.Request) {
	spec := parseWindow(request.URL.Query().Get("window"), s.now())
	writeJSON(response, http.StatusOK, s.buildSecurity(spec))
}

func (s *Server) handleOperations(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, s.buildOperations(request.Context()))
}

func (s *Server) handleCatalog(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, s.buildCatalog())
}

// ── Search ──────────────────────────────────────────────────────────────────

func (s *Server) handleSearch(response http.ResponseWriter, request *http.Request) {
	started := time.Now()
	query := strings.TrimSpace(request.URL.Query().Get("q"))
	limit := parseInt(request.URL.Query().Get("limit"), 6, 12)
	_, _, info := s.snapshot()
	results := []SearchResult{}
	if query == "" {
		writeJSON(response, http.StatusOK, SearchResponse{Query: query, Results: results, DurationMS: time.Since(started).Milliseconds(), Index: info})
		return
	}
	lower := strings.ToLower(query)
	orderedRuns, orderedChanges, _ := s.snapshot()
	type scored struct {
		result SearchResult
		score  int
	}
	scoredResults := []scored{}
	for _, record := range orderedRuns {
		manifest := record.Manifest
		fields := []string{record.ID, manifest.ChangeID, manifest.Repository, manifest.Scope, manifest.AgentHarness, manifest.SandboxID, manifest.BaselineSHA}
		score := searchScore(lower, fields)
		if score == 0 {
			continue
		}
		scoredResults = append(scoredResults, scored{score: score, result: SearchResult{
			Kind: "run", ID: record.ID, Title: record.ID, Status: string(manifest.FactoryResult),
			Subtitle: strings.TrimSpace(strings.Join(nonEmpty(manifest.Repository, manifest.AgentHarness, manifest.Scope), " · ")),
			Href:     detailHref("#/runs/", record.ID),
		}})
	}
	for _, record := range orderedChanges {
		change := record.Change
		fields := []string{change.ChangeID, change.Repository, change.Scope, change.Task, string(change.Status), change.LastRunID}
		score := searchScore(lower, fields)
		if score == 0 {
			continue
		}
		scoredResults = append(scoredResults, scored{score: score, result: SearchResult{
			Kind: "change", ID: change.ChangeID, Title: change.ChangeID, Status: string(change.Status),
			Subtitle: strings.TrimSpace(strings.Join(nonEmpty(change.Repository, change.Scope, string(change.Status)), " · ")),
			Href:     detailHref("#/changes/", change.ChangeID),
		}})
	}
	sort.SliceStable(scoredResults, func(i, j int) bool {
		if scoredResults[i].score == scoredResults[j].score {
			return scoredResults[i].result.ID > scoredResults[j].result.ID
		}
		return scoredResults[i].score > scoredResults[j].score
	})
	runCount, changeCount := 0, 0
	for _, item := range scoredResults {
		if item.result.Kind == "run" {
			if runCount >= limit {
				continue
			}
			runCount++
		} else {
			if changeCount >= limit {
				continue
			}
			changeCount++
		}
		results = append(results, item.result)
		if len(results) >= limit*2 {
			break
		}
	}
	writeJSON(response, http.StatusOK, SearchResponse{Query: query, Results: results, DurationMS: time.Since(started).Milliseconds(), Index: info})
}

func searchScore(lower string, fields []string) int {
	best := 0
	for _, field := range fields {
		candidate := strings.ToLower(field)
		if candidate == "" || !strings.Contains(candidate, lower) {
			continue
		}
		score := 1
		if strings.HasPrefix(candidate, lower) {
			score = 3
		}
		if candidate == lower {
			score = 5
		}
		if score > best {
			best = score
		}
	}
	return best
}

// ── Fiters and pagination ───────────────────────────────────────────────────

const (
	maxQueryLength = 200
	maxFacetValues = 20
	maxFacetLength = 120
)

// capQuery bounds free-text search so a token holder cannot make every request
// lower-case a megabyte once per run record.
func capQuery(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxQueryLength {
		return value[:maxQueryLength]
	}
	return value
}

func capValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxFacetLength {
		return value[:maxFacetLength]
	}
	return value
}

func splitValues(values []string) []string {
	out := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if len(part) > maxFacetLength {
				part = part[:maxFacetLength]
			}
			if len(out) >= maxFacetValues {
				return out
			}
			out = append(out, part)
		}
	}
	return out
}

func containsValue(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseOffset(cursor string) int {
	if strings.TrimSpace(cursor) == "" {
		return 0
	}
	value, err := strconv.Atoi(strings.TrimSpace(cursor))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func paginate[T any](items []T, offset, limit int) ([]T, PageMeta) {
	total := len(items)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := items[offset:end]
	meta := PageMeta{Limit: limit, Offset: offset, Total: total}
	if end < total {
		meta.NextCursor = strconv.Itoa(end)
	}
	return page, meta
}

func runMatchesQuery(record *runRecord, query string) bool {
	manifest := record.Manifest
	return containsInsensitive(query,
		record.ID, manifest.RunID, manifest.ChangeID, manifest.Repository,
		manifest.Scope, manifest.AgentHarness, manifest.SandboxID,
		manifest.VerificationProfile, manifest.BaselineSHA, manifest.RequestedRevision,
	)
}

func changeMatchesQuery(record *changeRecord, query string) bool {
	change := record.Change
	return containsInsensitive(query,
		change.ChangeID, change.Repository, change.Scope, change.Task,
		string(change.Status), change.LastRunID,
	)
}

func containsInsensitive(query string, fields ...string) bool {
	query = strings.ToLower(query)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func sortRuns(records []*runRecord, sortKey string) {
	key := strings.TrimSpace(sortKey)
	descending := true
	if strings.HasPrefix(key, "+") {
		descending = false
		key = key[1:]
	} else if strings.HasPrefix(key, "-") {
		key = key[1:]
	}
	switch key {
	case "duration", "duration_ms":
		sort.SliceStable(records, func(i, j int) bool {
			left, right := summaryDuration(records[i].Manifest), summaryDuration(records[j].Manifest)
			if descending {
				return left > right
			}
			return left < right
		})
	case "run_id":
		sort.SliceStable(records, func(i, j int) bool {
			if descending {
				return records[i].ID > records[j].ID
			}
			return records[i].ID < records[j].ID
		})
	case "completed_at":
		sort.SliceStable(records, func(i, j int) bool {
			left, right := records[i].Manifest.CompletedAt, records[j].Manifest.CompletedAt
			if descending {
				return left.After(right)
			}
			return left.Before(right)
		})
	default:
		sort.SliceStable(records, func(i, j int) bool {
			left, right := runSortTime(records[i]), runSortTime(records[j])
			if descending {
				return left.After(right)
			}
			return left.Before(right)
		})
	}
}

func sortChanges(records []*changeRecord, sortKey string) {
	key := strings.TrimSpace(sortKey)
	descending := true
	if strings.HasPrefix(key, "+") {
		descending = false
		key = key[1:]
	} else if strings.HasPrefix(key, "-") {
		key = key[1:]
	}
	switch key {
	case "created_at":
		sort.SliceStable(records, func(i, j int) bool {
			if descending {
				return records[i].Change.CreatedAt.After(records[j].Change.CreatedAt)
			}
			return records[i].Change.CreatedAt.Before(records[j].Change.CreatedAt)
		})
	case "attempts":
		sort.SliceStable(records, func(i, j int) bool {
			if descending {
				return records[i].Change.Attempts > records[j].Change.Attempts
			}
			return records[i].Change.Attempts < records[j].Change.Attempts
		})
	default:
		sort.SliceStable(records, func(i, j int) bool {
			if descending {
				return records[i].Change.UpdatedAt.After(records[j].Change.UpdatedAt)
			}
			return records[i].Change.UpdatedAt.Before(records[j].Change.UpdatedAt)
		})
	}
}
