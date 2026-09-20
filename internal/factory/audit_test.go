package factory

import (
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/artifacts"
)

func TestAppendAuditRedactsEveryFreeFormField(t *testing.T) {
	factory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	store, err := factory.ForRun("run-audit-redaction")
	if err != nil {
		t.Fatalf("ForRun: %v", err)
	}
	secret := "sk-1234567890abcdefghijklmnop"
	entry := AuditEntry{
		Action:  AuditReviewDecision,
		Actor:   "operator-" + secret,
		Error:   "request failed with " + secret,
		Detail:  map[string]any{"note": "please inspect " + secret, "nested": map[string]any{"token": secret}},
		Outcome: AuditOutcomeAttempted,
	}
	if err := AppendAudit(store, entry, NewRedactor(secret)); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	data, err := store.Read(ArtifactAudit)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("audit contains credential value: %s", data)
	}
	if !strings.Contains(string(data), RedactionMask) {
		t.Fatalf("audit does not show redaction marker: %s", data)
	}
}
