package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/threatmon"
)

// maxTripRulesBytes bounds an operator rules file. It is configuration, not
// evidence, so a generous but finite limit is appropriate.
const maxTripRulesBytes = 1 << 20

// BlockedFileName is the sentinel an agent writes to report that it cannot
// satisfy the task.
//
// The contract exists because an agent with no honest way to stop looks for a
// dishonest one. The report on the incident is explicit that impossible tasks
// plus relentless persistence is what pushed models into out-of-bounds probing;
// a documented, rewarded stop path removes the incentive rather than trying to
// detect its consequences.
const BlockedFileName = "blocked.json"

// BlockedFileDir is the factory metadata directory inside the sandbox.
const BlockedFileDir = ".factory"

// BlockedSignal is the parsed content of the sentinel.
type BlockedSignal struct {
	// Reason is the agent's short explanation. It is untrusted text.
	Reason string `json:"reason"`
	// Detail is optional longer context. It is untrusted text.
	Detail string `json:"detail,omitempty"`
	// Raw is the bounded original content, kept because an agent may not follow
	// the schema and the operator still needs to see what it said.
	Raw string `json:"raw,omitempty"`
}

// BlockedFilePathFor returns the in-sandbox path of the sentinel for a
// repository directory. It lives beside the repository, like the inventory, so
// it can never become part of the deliverable patch.
func BlockedFilePathFor(repositoryDir string) string {
	return path.Join(metadataDirFor(repositoryDir), BlockedFileDir, BlockedFileName)
}

// readBlockedSignal returns the parsed sentinel, or ok=false when the agent did
// not report blocked.
//
// A missing file is the normal case and is not an error. Any other read failure
// is returned, because "permission denied" is not the same fact as "the agent
// did not report blocked" and silently dropping it would hide a signal the
// agent tried to send.
func readBlockedSignal(ctx context.Context, sb sandbox.Sandbox, repositoryDir string) (BlockedSignal, bool, error) {
	raw, err := sb.ReadFile(ctx, BlockedFilePathFor(repositoryDir))
	if err != nil {
		if isNotFound(err) {
			return BlockedSignal{}, false, nil
		}
		return BlockedSignal{}, false, err
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return BlockedSignal{}, false, nil
	}
	signal := BlockedSignal{Raw: boundBlockedText(text)}
	var parsed struct {
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err == nil && (parsed.Reason != "" || parsed.Detail != "") {
		signal.Reason = boundBlockedText(parsed.Reason)
		signal.Detail = boundBlockedText(parsed.Detail)
		return signal, true, nil
	}
	// Plain text is accepted: the contract is about the agent being able to
	// stop, not about the agent producing valid JSON.
	signal.Reason = boundBlockedText(text)
	return signal, true, nil
}

// boundBlockedText keeps an untrusted sentinel small. The text is redacted
// before it reaches evidence by the same writer that handles every artifact.
func boundBlockedText(text string) string {
	const limit = 2048
	text = strings.TrimSpace(text)
	if len(text) > limit {
		return text[:limit] + "...(truncated)"
	}
	return text
}

// tripRuleFile mirrors threatmon.Rule in a JSON-friendly form accepted from
// operator configuration. A compiled regexp cannot be unmarshalled directly.
type tripRuleFile struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	Pattern     string `json:"pattern"`
}

// loadOperatorRules reads additional operator rules. They are ADDED to the
// built-in ruleset; an operator cannot use this file to weaken detection.
func (a *Activities) loadOperatorRules() ([]threatmon.Rule, error) {
	filePath := strings.TrimSpace(a.cfg.Hardening.TripRulesFile)
	if filePath == "" {
		return nil, nil
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("read trip rules %s: %w", filePath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("trip rules %s must be a regular file", filePath)
	}
	if info.Size() > maxTripRulesBytes {
		return nil, fmt.Errorf("trip rules %s exceeds %d bytes", filePath, maxTripRulesBytes)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read trip rules %s: %w", filePath, err)
	}
	var parsed []tripRuleFile
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, fmt.Errorf("parse trip rules %s: %w", filePath, err)
	}
	rules := make([]threatmon.Rule, 0, len(parsed))
	for _, r := range parsed {
		pattern, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("trip rule %q: %w", r.ID, err)
		}
		rules = append(rules, threatmon.Rule{
			ID:          r.ID,
			Category:    r.Category,
			Description: r.Description,
			Severity:    threatmon.Severity(r.Severity),
			Pattern:     pattern,
		})
	}
	if err := threatmon.ValidateRules(rules); err != nil {
		return nil, fmt.Errorf("trip rules %s: %w", filePath, err)
	}
	return rules, nil
}

// newBehaviorMonitor builds a monitor from the built-in ruleset plus any
// operator rules.
//
// Operator misconfiguration must not silently disable detection, so a broken
// rules file falls back to the built-in set and reports the problem.
func (a *Activities) newBehaviorMonitor() (*threatmon.Monitor, error) {
	rules := threatmon.DefaultRules()
	extra, err := a.loadOperatorRules()
	if err != nil {
		return threatmon.New(rules, threatmon.Severity(a.cfg.Hardening.TripLevel())), err
	}
	return threatmon.New(append(rules, extra...), threatmon.Severity(a.cfg.Hardening.TripLevel())), nil
}

// scanAgentBehavior runs the deterministic behavior monitor over the agent's
// captured output.
//
// The monitor is post-hoc within the run: the sandbox data plane returns
// buffered output, so the factory cannot observe a line before the agent exits.
// It still runs BEFORE verification and before any success is recorded, which
// is what stops a flagged run from being gated and shipped. Live interruption
// requires provider-side output streaming and is tracked separately.
func (a *Activities) scanAgentBehavior(ctx context.Context, stdout, stderr string) (threatmon.Report, error) {
	monitor, err := a.newBehaviorMonitor()
	monitor.Observe("stdout", stdout)
	monitor.Observe("stderr", stderr)
	return monitor.Report(), err
}

// ScanPatchInput identifies the run whose collected patch should be scanned.
type ScanPatchInput struct {
	RunID string `json:"run_id"`
	// AllowGateSelfModification suppresses the gate-tamper rules. The operator
	// has explicitly permitted gate changes, so the monitor must not quarantine
	// a run for exactly the thing the configuration allows. The tree-based gate
	// check is skipped under the same flag, so the two agree.
	AllowGateSelfModification bool `json:"allow_gate_self_modification,omitempty"`
}

// ScanPatchOutput carries the patch scan and any non-fatal problem.
type ScanPatchOutput struct {
	Report *threatmon.Report `json:"report,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// ScanPatch runs the behavior monitor over the collected patch.
//
// The agent's own output is not the only place a rule can match: a patch that
// rewrites a build script, adds a reverse shell to a Makefile, or embeds a
// credential is visible only in the diff. Scanning it here means the patch
// rules are wired to real data instead of being unreachable.
func (a *Activities) ScanPatch(ctx context.Context, in ScanPatchInput) (ScanPatchOutput, error) {
	if a.artifactFactory == nil {
		return ScanPatchOutput{}, nil
	}
	store, err := a.artifactFactory.ForRun(in.RunID)
	if err != nil {
		return ScanPatchOutput{}, err
	}
	patch, err := store.Read(ArtifactPatch)
	if err != nil {
		// The patch artifact is missing because collection failed, which is
		// recorded elsewhere. The scan could not run, and that is reported
		// rather than presented as a clean patch.
		return ScanPatchOutput{Error: "patch artifact unavailable: " + a.redactor.Redact(err.Error())}, nil
	}
	monitor, ruleErr := a.newBehaviorMonitor()
	monitor.Observe("patch", string(patch))
	report := monitor.Report()
	if in.AllowGateSelfModification {
		report = dropFindings(report, gateTamperRuleIDs, threatmon.Severity(a.cfg.Hardening.TripLevel()))
	}
	out := ScanPatchOutput{Report: &report}
	if ruleErr != nil {
		out.Error = a.redactor.Redact(ruleErr.Error())
	}
	if err := a.writeJSON(store, ArtifactPatchScan, report); err != nil {
		out.Error = appendError(out.Error, a.redactor.Redact(err.Error()))
	}
	return out, nil
}

// gateTamperRuleIDs are the monitor rules that duplicate the tree-based gate
// check. They stay in the ruleset for defense in depth (they also match
// wrappers and CI files the declared gate list does not name) and are dropped
// only when the operator has explicitly allowed gate self-modification.
var gateTamperRuleIDs = map[string]bool{"BG-001": true, "BG-002": true}

// dropFindings removes the named rules and recomputes the verdict, so a report
// never keeps a trip that no surviving finding supports.
func dropFindings(report threatmon.Report, drop map[string]bool, trip threatmon.Severity) threatmon.Report {
	kept := make([]threatmon.Finding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		if drop[finding.RuleID] {
			continue
		}
		kept = append(kept, finding)
	}
	report.Findings = kept
	report.Tripped = false
	report.MaxSeverity = ""
	for _, finding := range kept {
		if finding.Severity.Rank() > report.MaxSeverity.Rank() {
			report.MaxSeverity = finding.Severity
		}
		if finding.Severity.Rank() >= trip.Rank() {
			report.Tripped = true
		}
	}
	return report
}

// GateIntegrityScriptDescription identifies the factory-owned gate check
// command, so a provider or test can recognise it.
const GateIntegrityScriptDescription = "factory gate integrity check"

// repoGatePaths filters the configured gate programs down to paths that live
// inside the repository. An absolute program is outside the checkout and cannot
// be modified by a patch, so it is not a gate-integrity concern.
func repoGatePaths(gatePaths []string) []string {
	out := make([]string, 0, len(gatePaths))
	for _, p := range gatePaths {
		normalized := normalizeGatePath(p)
		if normalized == "" || strings.HasPrefix(normalized, "/") {
			continue
		}
		out = append(out, normalized)
	}
	sort.Strings(out)
	return out
}

// gateIntegrityScript builds factory-owned shell code that compares each gate
// file's CONTENT against its blob at the baseline revision.
//
// The check deliberately does not use a diff. `git diff` is influenced by
// repository-local state an agent can set without any of it appearing in the
// patch: `--assume-unchanged` hides a file from `git add` and `git diff`, a
// committed tamper leaves the staged diff empty, and `diff.mnemonicPrefix`
// rewrites the header paths. `git hash-object` reads the file itself, and
// `git rev-parse <rev>:<path>` is the baseline blob, so the comparison is
// immune to all three.
func gateIntegrityScript(repoDir, baseline string, paths []string) (string, error) {
	dirWord, err := quoteShellWord(repoDir)
	if err != nil {
		return "", fmt.Errorf("repository directory: %w", err)
	}
	var b strings.Builder
	b.WriteString("set -u\n")
	fmt.Fprintf(&b, "cd %s 2>/dev/null || exit 3\n", dirWord)
	for _, p := range paths {
		specWord, err := quoteShellWord(baseline + ":" + p)
		if err != nil {
			return "", fmt.Errorf("gate path %q: %w", p, err)
		}
		pathWord, err := quoteShellWord(p)
		if err != nil {
			return "", fmt.Errorf("gate path %q: %w", p, err)
		}
		fmt.Fprintf(&b, "expected=$(git rev-parse --verify -q %s 2>/dev/null || true)\n", specWord)
		fmt.Fprintf(&b, "actual=$(git hash-object -- %s 2>/dev/null || true)\n", pathWord)
		fmt.Fprintf(&b, "printf 'ESF_GATE %%s %%s %%s\\n' \"${expected:-none}\" \"${actual:-none}\" %s\n", pathWord)
	}
	b.WriteString("exit 0\n")
	return b.String(), nil
}

// parseGateIntegrity reads the check's output.
//
// A gate the script did not report is treated as MODIFIED. A missing line means
// the check could not decide, and an undecided gate must not be reported as
// clean: that is exactly the fail-open shape this control exists to avoid.
func parseGateIntegrity(stdout string, paths []string) []string {
	expected := map[string]bool{}
	for _, p := range paths {
		expected[p] = true
	}
	reported := map[string]bool{}
	modified := map[string]bool{}
	for _, line := range strings.Split(stdout, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ESF_GATE ")
		if !ok {
			continue
		}
		fields := strings.SplitN(rest, " ", 3)
		if len(fields) != 3 {
			continue
		}
		before, after, p := fields[0], fields[1], strings.TrimSpace(fields[2])
		if !expected[p] {
			continue
		}
		reported[p] = true
		if before != after {
			modified[p] = true
		}
	}
	for p := range expected {
		if !reported[p] {
			modified[p] = true
		}
	}
	out := make([]string, 0, len(modified))
	for p := range modified {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// quoteShellWord single-quotes one word so the shell cannot reinterpret it.
//
// It refuses an empty word or one containing a newline or NUL: those have no
// legitimate place in a repository path, and silently mangling them would make
// the check lie.
func quoteShellWord(word string) (string, error) {
	if word == "" {
		return "", fmt.Errorf("empty word")
	}
	if strings.ContainsAny(word, "\n\r\x00") {
		return "", fmt.Errorf("word contains a newline or NUL")
	}
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'", nil
}

// isNotFound reports whether a sandbox read error means "the file is not there".
//
// The provider interface does not expose a typed not-found error, so this
// matches the portable phrasings. It is deliberately narrow: a permission error
// or a transport failure must not be mistaken for the normal case.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"no such file", "not found", "does not exist", "filenotfound", "enoent"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
