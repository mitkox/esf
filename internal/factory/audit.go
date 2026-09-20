package factory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/mitkox/esf/internal/artifacts"
)

// Operator actions that touch a live sandbox are privileged, so they are
// recorded in the run's evidence. Attach is remote code execution with the
// factory's credentials; preview publishes a sandbox to the ingress. Neither
// should be invisible after the fact.
//
// The trail is append-only JSONL rather than a single JSON document, because
// two operators may act on the same run and an audit log that can silently drop
// one of them is not an audit log.

// AuditAction identifies a privileged operator action.
type AuditAction string

const (
	// AuditOutcomeAttempted records intent before a privileged action runs, so
	// a crash during the action cannot erase the fact it was invoked.
	AuditOutcomeAttempted = "ATTEMPTED"
	// AuditAttach records `factory attach`.
	AuditAttach AuditAction = "attach"
	// AuditPreview records `factory preview`.
	AuditPreview AuditAction = "preview"
	// AuditSuspend records a sandbox checkpoint.
	AuditSuspend AuditAction = "suspend"
	// AuditResume records a sandbox wake.
	AuditResume AuditAction = "resume"
	// AuditReviewDecision records a human decision sent to a paused run.
	AuditReviewDecision AuditAction = "review-decision"
)

// AuditEntry is one privileged action.
type AuditEntry struct {
	// At is wall-clock time: an audit trail is about when a human acted, so it
	// deliberately does not use workflow time.
	At time.Time `json:"at"`
	// Action is what was done.
	Action AuditAction `json:"action"`
	// RunID and SandboxID identify the target.
	RunID     string `json:"run_id"`
	SandboxID string `json:"sandbox_id,omitempty"`
	// Actor is the operating-system user that invoked the action. It is a
	// best-effort attribution, not authentication: ESF assumes the CLI runs
	// under an operator identity.
	Actor string `json:"actor"`
	// Outcome is SUCCESS, FAILED or SKIPPED.
	Outcome string `json:"outcome"`
	// Detail carries action-specific, non-secret context (a port, an argv
	// length, a reason). Command output is deliberately NOT stored here.
	Detail map[string]any `json:"detail,omitempty"`
	// Error is a redacted failure description.
	Error string `json:"error,omitempty"`
}

// AuditPath is the store-relative path of the audit trail.
const AuditPath = ArtifactAudit

// AppendAudit appends an entry to a run's audit trail.
//
// The file is opened with O_APPEND so concurrent writers cannot truncate each
// other, and it is created 0600 like every other artifact.
func AppendAudit(store artifacts.Store, entry AuditEntry, redactors ...*Redactor) error {
	var redactor *Redactor
	if len(redactors) > 0 {
		redactor = redactors[0]
	}
	entry = redactAuditEntry(entry, redactor)
	if entry.At.IsZero() {
		entry.At = time.Now().UTC()
	}
	if entry.Actor == "" {
		entry.Actor = currentActor()
	}
	if entry.Outcome == "" {
		entry.Outcome = "SUCCESS"
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("audit: encode entry: %w", err)
	}
	dir := store.Dir()
	path := filepath.Join(dir, AuditPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("audit: create directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("audit: append: %w", err)
	}
	return f.Sync()
}

// redactAuditEntry applies the same evidence redactor used for manifests and
// logs to every free-form audit value. Structured numeric fields remain typed.
func redactAuditEntry(entry AuditEntry, redactor *Redactor) AuditEntry {
	entry.Actor = redactor.Redact(entry.Actor)
	entry.Error = redactor.Redact(entry.Error)
	entry.Detail = redactAuditMap(entry.Detail, redactor)
	return entry
}

func redactAuditMap(values map[string]any, redactor *Redactor) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			out[key] = redactor.Redact(typed)
		case map[string]any:
			out[key] = redactAuditMap(typed, redactor)
		case []string:
			redacted := make([]string, len(typed))
			for i, item := range typed {
				redacted[i] = redactor.Redact(item)
			}
			out[key] = redacted
		case []any:
			redacted := make([]any, len(typed))
			for i, item := range typed {
				if text, ok := item.(string); ok {
					redacted[i] = redactor.Redact(text)
				} else {
					redacted[i] = item
				}
			}
			out[key] = redacted
		default:
			out[key] = value
		}
	}
	return out
}

// ReadAudit returns the audit trail, oldest first. A missing trail is not an
// error: most runs are never touched by an operator.
func ReadAudit(store artifacts.Store) ([]AuditEntry, error) {
	data, err := os.ReadFile(filepath.Join(store.Dir(), AuditPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("audit: read: %w", err)
	}
	var entries []AuditEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			// A corrupt line must not hide the rest of the trail.
			entries = append(entries, AuditEntry{Action: "unreadable", Error: err.Error()})
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("audit: scan: %w", err)
	}
	return entries, nil
}

// currentActor identifies the operator for the audit trail.
func currentActor() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	for _, name := range []string{"USER", "LOGNAME"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return "unknown"
}
