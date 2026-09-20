package factory

import (
	"encoding/json"
	"strings"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/artifacts"
	"testing"
)

// TestRedactorScrubsPatterns is a security test: captured agent output is
// written to durable evidence, so a credential echoed by an agent must not
// survive into storage.
func TestRedactorScrubsPatterns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		input      string
		mustNotSee string
	}{
		{
			name:       "bearer token",
			input:      "curl -H 'Authorization: Bearer abc123def456ghi789' https://api.example", // gitleaks:allow -- synthetic redaction fixture
			mustNotSee: "abc123def456ghi789",
		},
		{
			name:       "openai style key",
			input:      "export OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz012345",
			mustNotSee: "sk-proj-abcdefghijklmnopqrstuvwxyz012345",
		},
		{
			name:       "github token",
			input:      "remote: https://ghp_abcdefghijklmnopqrstuvwxyz0123456789@github.com/o/r",
			mustNotSee: "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		},
		{
			name:       "aws access key id",
			input:      "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			mustNotSee: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:       "url embedded credentials",
			input:      "fatal: could not read from https://user:hunter2secret@git.example.com/repo.git",
			mustNotSee: "hunter2secret",
		},
		{
			name:       "sensitive assignment",
			input:      "DATABASE_PASSWORD=supersecretvalue",
			mustNotSee: "supersecretvalue",
		},
		{
			name:       "slack token",
			input:      "SLACK=xoxb-1234567890-abcdefghijkl",
			mustNotSee: "xoxb-1234567890-abcdefghijkl",
		},
	}

	r := NewRedactor()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := r.Redact(tc.input)
			if strings.Contains(got, tc.mustNotSee) {
				t.Fatalf("secret survived redaction:\ninput:  %s\noutput: %s", tc.input, got)
			}
			if !strings.Contains(got, RedactionMask) {
				t.Fatalf("expected the redaction mask in %q", got)
			}
		})
	}
}

func TestCollectSecretsIncludesNamedModelCredential(t *testing.T) {
	secret := "named-model-secret-value"
	t.Setenv("FACTORY_NAMED_MODEL_KEY", secret)
	cfg := Config{Models: map[string]ModelConfig{
		"fast": {Provider: "test", Model: "model", APIKeyEnv: "FACTORY_NAMED_MODEL_KEY"},
	}}
	values := collectSecrets(cfg, agentharness.NewRegistry())
	if len(values) != 1 || values[0] != secret {
		t.Fatalf("collectSecrets = %v, want named model credential", values)
	}
}

func TestRedactorScrubsRegisteredValues(t *testing.T) {
	t.Parallel()
	// A value with no recognisable shape must still be scrubbed when the
	// operator has registered it.
	const secret = "opaque-token-value-9876"
	r := NewRedactor(secret)
	got := r.Redact("the agent printed " + secret + " verbatim")
	if strings.Contains(got, secret) {
		t.Fatalf("registered secret survived: %q", got)
	}
	if r.ValueCount() != 1 {
		t.Fatalf("ValueCount = %d, want 1", r.ValueCount())
	}
}

func TestRedactorIgnoresShortAndEmptyValues(t *testing.T) {
	t.Parallel()
	// Masking a very short value would corrupt unrelated text.
	r := NewRedactor("", "ab", "   ")
	if r.ValueCount() != 0 {
		t.Fatalf("ValueCount = %d, want 0", r.ValueCount())
	}
	if got := r.Redact("abcabc"); got != "abcabc" {
		t.Fatalf("short text was altered: %q", got)
	}
}

func TestRedactorIsNilSafe(t *testing.T) {
	t.Parallel()
	var r *Redactor
	// Callers must not need to branch on whether redaction is configured.
	r.Add("something")
	if got := r.Redact(""); got != "" {
		t.Fatalf("Redact(\"\") = %q", got)
	}
	if r.ValueCount() != 0 {
		t.Fatalf("nil receiver ValueCount = %d", r.ValueCount())
	}
}

func TestRedactorPreservesDiagnosticContext(t *testing.T) {
	t.Parallel()
	r := NewRedactor()
	got := r.Redact("Authorization: Bearer somelongtokenvalue")
	// The header name is still visible, so the evidence shows WHAT leaked.
	if !strings.Contains(strings.ToLower(got), "authorization") {
		t.Fatalf("redaction destroyed the diagnostic context: %q", got)
	}
	if strings.Contains(got, "somelongtokenvalue") {
		t.Fatalf("token survived: %q", got)
	}
}

func TestJSONEvidenceRedactionPreservesEncoding(t *testing.T) {
	const secret = "opaque\"secret\nwith-newline"
	factory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := factory.ForRun("run-redaction")
	if err != nil {
		t.Fatal(err)
	}
	acts := &Activities{redactor: NewRedactor(secret)}
	input := map[string]any{
		"nested": []any{map[string]any{"output": "PASSWORD=example-secret " + secret}},
		"count":  int64(9007199254740993),
	}
	if err := acts.writeJSON(store, "evidence.json", input); err != nil {
		t.Fatal(err)
	}
	data, err := store.Read("evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("invalid JSON: %s", data)
	}
	var got struct {
		Nested []struct {
			Output string `json:"output"`
		} `json:"nested"`
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Nested[0].Output, secret) || strings.Contains(got.Nested[0].Output, "example-secret") {
		t.Fatal("secret survived in JSON evidence")
	}
	if got.Count != 9007199254740993 {
		t.Fatalf("integer precision lost: %d", got.Count)
	}
}
