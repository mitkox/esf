// Package threatmon implements a deterministic, dependency-free behaviour
// monitor for captured agent output.
//
// ESF runs coding agents inside isolated microVMs. This package scans the text
// an agent produces — stdout, stderr, and patch bodies — for indicators of
// out-of-bounds or offensive behaviour, so the factory can quarantine a run
// automatically and alert. There is deliberately no model in the loop: every
// finding is attributable to a named Rule with a pre-compiled regular
// expression, and the Report for identical input is identical, including the
// order of findings.
//
// Typical use:
//
//	m := threatmon.New(threatmon.DefaultRules(), threatmon.SeverityHigh)
//	m.Observe("stdout", outChunk)
//	m.Observe("stderr", errChunk)
//	if r := m.Report(); r.Tripped {
//		// Quarantine the run; r.MaxSeverity and r.Findings are the evidence.
//	}
//
// A Monitor is not safe for concurrent use.
package threatmon

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Severity is the impact class of a rule.
type Severity string

// Severity levels, ordered from least to most severe.
const (
	SeverityLow      Severity = "LOW"
	SeverityMedium   Severity = "MEDIUM"
	SeverityHigh     Severity = "HIGH"
	SeverityCritical Severity = "CRITICAL"
)

// RulesetVersion identifies the shipped default ruleset. It is recorded in
// every Report so a quarantine decision can be reproduced against the exact
// rules that produced it.
const RulesetVersion = "1"

// Rank orders severities: LOW=1, MEDIUM=2, HIGH=3, CRITICAL=4. An unknown
// severity ranks 0, which is below every valid severity.
func (s Severity) Rank() int {
	switch s {
	case SeverityLow:
		return 1
	case SeverityMedium:
		return 2
	case SeverityHigh:
		return 3
	case SeverityCritical:
		return 4
	default:
		return 0
	}
}

// Valid reports whether s is one of the four defined severities.
func (s Severity) Valid() bool {
	return s.Rank() != 0
}

// Rule is one deterministic detector.
//
// Pattern is matched against a single line at a time. A rule contributes at
// most one Finding per line even when its pattern matches several times.
type Rule struct {
	// ID is the stable identifier recorded in findings and evidence, for
	// example "PE-001". It must be unique within a ruleset.
	ID string
	// Category groups rules by behaviour, for example "privilege-escalation".
	Category string
	// Description explains the signal in prose for a human reviewing a
	// quarantine.
	Description string
	// Severity is the impact class assigned to a match.
	Severity Severity
	// Pattern is the compiled detector. It must not be nil.
	Pattern *regexp.Regexp
}

// Finding is one rule match on one line of one stream.
type Finding struct {
	RuleID   string   `json:"rule_id"`
	Category string   `json:"category"`
	Severity Severity `json:"severity"`
	Stream   string   `json:"stream"`
	Line     int      `json:"line"`
	Excerpt  string   `json:"excerpt"`
}

// Report is the accumulated, deterministic result of a scan.
//
// Findings is ordered by (stream, line, rule ID) and is never nil, so two
// reports for identical input compare equal with reflect.DeepEqual.
type Report struct {
	RulesetVersion string    `json:"ruleset_version"`
	ScannedLines   int       `json:"scanned_lines"`
	Findings       []Finding `json:"findings"`
	Tripped        bool      `json:"tripped"`
	MaxSeverity    Severity  `json:"max_severity,omitempty"`
}

// maxExcerptBytes bounds every Finding.Excerpt. The "..." truncation marker is
// included in the bound, so an excerpt is never longer than this many bytes.
const maxExcerptBytes = 200

// DefaultRules returns a fresh copy of the shipped ruleset. See rules.go for
// the individual detectors and their false-positive notes.
func DefaultRules() []Rule { return defaultRules() }

// ValidateRules strictly checks a ruleset: every rule must have a non-empty
// ID, a unique ID, a non-nil pattern, and a valid severity. It is intended for
// tests and configuration validation; New is the tolerant runtime path.
func ValidateRules(rules []Rule) error {
	seen := make(map[string]struct{}, len(rules))
	for i, r := range rules {
		if strings.TrimSpace(r.ID) == "" {
			return fmt.Errorf("threatmon: rule at index %d has an empty ID", i)
		}
		if _, dup := seen[r.ID]; dup {
			return fmt.Errorf("threatmon: duplicate rule ID %q (index %d)", r.ID, i)
		}
		seen[r.ID] = struct{}{}
		if r.Pattern == nil {
			return fmt.Errorf("threatmon: rule %q has a nil pattern", r.ID)
		}
		if !r.Severity.Valid() {
			return fmt.Errorf("threatmon: rule %q has invalid severity %q", r.ID, string(r.Severity))
		}
	}
	return nil
}

// Monitor accumulates findings across incremental Observe calls.
type Monitor struct {
	rules []Rule

	// trip is the severity at or above which Report.Tripped becomes true.
	trip Severity

	// lines counts scanned lines per stream so numbering continues across
	// Observe calls. It is keyed by stream name and only ever read for one
	// stream at a time while scanning, never iterated, so it cannot perturb
	// determinism.
	lines map[string]int

	findings     []Finding
	scannedLines int
}

// New returns a Monitor that records findings for every rule match and marks
// the report as tripped when a finding's severity ranks at or above trip.
//
// New is tolerant: it is a runtime path and never panics. Rules with an empty
// ID, a duplicate ID, a nil pattern, or an invalid severity are skipped (the
// first rule with a given ID wins). Use ValidateRules to reject a malformed
// ruleset strictly before construction.
//
// An invalid trip severity ranks 0, so any finding trips the monitor.
func New(rules []Rule, trip Severity) *Monitor {
	accepted := make([]Rule, 0, len(rules))
	seen := make(map[string]struct{}, len(rules))
	for _, r := range rules {
		if strings.TrimSpace(r.ID) == "" {
			continue
		}
		if _, dup := seen[r.ID]; dup {
			continue
		}
		if r.Pattern == nil || !r.Severity.Valid() {
			continue
		}
		seen[r.ID] = struct{}{}
		accepted = append(accepted, r)
	}
	return &Monitor{
		rules:    accepted,
		trip:     trip,
		lines:    make(map[string]int),
		findings: make([]Finding, 0),
	}
}

// Observe scans one chunk of text from a named stream ("stdout", "stderr",
// "patch", ...). The text is split on newlines, and every line is scanned
// against every rule once. An empty line is still counted.
//
// Line numbering is per stream, starts at 1, and continues across calls so a
// caller can feed chunks incrementally. A trailing newline terminates a line
// rather than starting an empty one, and a final line without a trailing
// newline is counted. Observe does not join a partial line with the next
// call's text, so callers should feed complete lines; Observe(stream, "")
// contributes no lines.
func (m *Monitor) Observe(stream, text string) {
	if m == nil {
		return
	}
	for _, raw := range splitLines(text) {
		line := normalizeLine(raw)
		m.scannedLines++
		m.lines[stream]++
		lineNo := m.lines[stream]

		for _, rule := range m.rules {
			loc := rule.Pattern.FindStringIndex(line)
			if loc == nil {
				continue
			}
			m.findings = append(m.findings, Finding{
				RuleID:   rule.ID,
				Category: rule.Category,
				Severity: rule.Severity,
				Stream:   stream,
				Line:     lineNo,
				// Excerpt from the first match to end of line: the first
				// match locates it, bounded by maxExcerptBytes.
				Excerpt: makeExcerpt(line[loc[0]:]),
			})
		}
	}
}

// Report returns the accumulated report. Findings are ordered by
// (stream, line, rule ID). Tripped is true when any finding's severity ranks at
// or above the trip threshold. The returned slice is a copy, so mutating it
// does not affect the Monitor.
func (m *Monitor) Report() Report {
	if m == nil {
		return Report{
			RulesetVersion: RulesetVersion,
			Findings:       make([]Finding, 0),
		}
	}

	out := make([]Finding, len(m.findings))
	copy(out, m.findings)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stream != out[j].Stream {
			return out[i].Stream < out[j].Stream
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RuleID < out[j].RuleID
	})

	rep := Report{
		RulesetVersion: RulesetVersion,
		ScannedLines:   m.scannedLines,
		Findings:       out,
	}
	for _, f := range out {
		if f.Severity.Rank() > rep.MaxSeverity.Rank() {
			rep.MaxSeverity = f.Severity
		}
		if f.Severity.Rank() >= m.trip.Rank() {
			rep.Tripped = true
		}
	}
	return rep
}

// splitLines splits text into the lines Observe scans. A single trailing
// newline terminates the last line instead of starting an empty one, so
// "a\nb\n" and "a\nb" both yield two lines and "\n" yields one empty line.
//
// Backslash line continuations are joined first. A shell command split across
// two lines ("su\" then "do ...") executes as `sudo ...`, so matching the raw
// lines would let an agent split every keyword it wants to hide.
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	joined := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
			line = line[:len(line)-1] + lines[i+1]
			i++
		}
		joined = append(joined, line)
	}
	return joined
}

// normalizeLine removes the byte-level noise an agent can use to break a
// keyword without changing what a shell does with it.
//
// ANSI escape sequences are stripped (`su\x1b[0mdo` is `sudo`), carriage
// returns are dropped (`cu\rrl` is `curl`), and other C0 control bytes and DEL
// become spaces. Matching runs on the normalized line, while the excerpt is
// taken from the same normalized text so evidence shows what was matched.
func normalizeLine(line string) string {
	if !needsNormalization(line) {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	for i := 0; i < len(line); {
		c := line[i]
		switch {
		case c == 0x1b:
			// CSI/OSC/other escape sequence: skip the introducer and its
			// parameter bytes, then the final byte. A bare ESC is dropped on
			// its own; the byte after it is data, not part of the sequence.
			i++
			if i < len(line) && (line[i] == '[' || line[i] == ']') {
				i++
				for i < len(line) && (line[i] < 0x40 || line[i] > 0x7e) {
					i++
				}
				if i < len(line) {
					i++
				}
			}
		case c == '\r':
			i++
		case c < 0x20 || c == 0x7f:
			b.WriteByte(' ')
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// needsNormalization is a fast path: most lines contain no control bytes, and
// the scanner runs over every captured line.
func needsNormalization(line string) bool {
	for i := 0; i < len(line); i++ {
		if line[i] < 0x20 || line[i] == 0x7f {
			return true
		}
	}
	return false
}

// makeExcerpt sanitizes and bounds an excerpt. Newlines and tabs become single
// spaces, and a value longer than maxExcerptBytes is cut (on a UTF-8 boundary)
// with a trailing "...", which is included in the bound. The result never
// exceeds maxExcerptBytes bytes and never contains a newline or tab.
func makeExcerpt(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")

	if len(s) <= maxExcerptBytes {
		return s
	}
	cut := maxExcerptBytes - len("...")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
