package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemotePolicyRejectsHostAccess is the security boundary test for
// repository input: a caller may name an approved repository, nothing else.
func TestRemotePolicyRejectsHostAccess(t *testing.T) {
	t.Parallel()

	allowed := []string{"https://github.com/my-org/"}
	p := New(allowed)

	cases := []struct {
		name    string
		url     string
		rev     string
		wantErr bool
	}{
		{
			name: "approved repository",
			url:  "https://github.com/my-org/service",
			rev:  "main",
		},
		{
			name:    "unapproved host",
			url:     "https://evil.example.com/repo",
			rev:     "main",
			wantErr: true,
		},
		{
			name:    "unapproved organisation",
			url:     "https://github.com/other-org/repo",
			rev:     "main",
			wantErr: true,
		},
		{
			// file:// would let a caller read the factory host.
			name:    "local file scheme",
			url:     "file:///etc/passwd",
			rev:     "main",
			wantErr: true,
		},
		{
			name:    "bare path",
			url:     "/etc/passwd",
			rev:     "main",
			wantErr: true,
		},
		{
			// Credentials must never travel inside a URL: they leak into process
			// listings and logs.
			name:    "embedded credentials",
			url:     "https://user:token@github.com/my-org/repo",
			rev:     "main",
			wantErr: true,
		},
		{
			name:    "missing revision",
			url:     "https://github.com/my-org/service",
			rev:     "",
			wantErr: true,
		},
		{
			// A revision starting with '-' would be parsed as a git option.
			name:    "option-like revision",
			url:     "https://github.com/my-org/service",
			rev:     "--upload-pack=touch /tmp/pwned",
			wantErr: true,
		},
		{
			name:    "revision with newline",
			url:     "https://github.com/my-org/service",
			rev:     "main\nrm -rf /",
			wantErr: true,
		},
		{
			name:    "missing url",
			url:     "",
			rev:     "main",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := p.Validate(Request{
				Kind:     SourceRemote,
				URL:      tc.url,
				Revision: tc.rev,
			})
			if tc.wantErr && err == nil {
				t.Fatal("expected the request to be rejected")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrRepositoryRejected) {
				t.Fatalf("expected ErrRepositoryRejected, got %v", err)
			}
		})
	}
}

func TestRemoteRequiresAnAllowlist(t *testing.T) {
	t.Parallel()
	// With no allowlist configured, no remote repository is reachable. Local
	// sources remain available.
	p := New(nil)
	if err := p.Validate(Request{Kind: SourceRemote, URL: "https://github.com/a/b", Revision: "main"}); err == nil {
		t.Fatal("expected remotes to be disallowed when no allowlist is configured")
	}
}

func TestRemotePolicyMatchesAuthorityAndPathBoundaries(t *testing.T) {
	for _, tc := range []struct {
		prefix, value string
		allowed       bool
	}{
		{"https://github.com", "https://github.com.evil.example/acme/repo", false},
		{"https://github.com/acme", "https://github.com/acme-private/repo", false},
		{"https://github.com/acme/service", "https://github.com/acme/service-private", false},
		{"https://github.com/acme/", "https://github.com/acme/../private/repo", false},
		{"https://github.com/acme/", "https://github.com/acme/%2e%2e/private/repo", false},
		{"https://github.com/acme/", "https://github.com/acme/repo?token=secret", false},
		{"https://github.com/acme/", "https://github.com:8443/acme/repo", false},
		{"https://github.com/acme/", "https://GITHUB.com:443/acme/repo", true},
		{"https://github.com/acme/service", "https://github.com/acme/service", true},
		{"https://github.com/acme/", "https://github.com/acme/service", true},
	} {
		err := New([]string{tc.prefix}).Validate(Request{Kind: SourceRemote, URL: tc.value, Revision: "main"})
		if (err == nil) != tc.allowed {
			t.Errorf("prefix %q URL %q: error=%v, allowed=%v", tc.prefix, tc.value, err, tc.allowed)
		}
	}
}

func TestLocalSourceValidation(t *testing.T) {
	t.Parallel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	approved := filepath.Join(dir, "approved")
	if err := os.Mkdir(approved, 0700); err != nil {
		t.Fatal(err)
	}
	req := Request{Kind: SourceLocal, LocalPath: approved, Revision: "abc123"}
	if err := New(nil).Validate(req); !errors.Is(err, ErrRepositoryRejected) {
		t.Fatalf("unregistered source: %v", err)
	}
	p := New(nil, WithLocalPaths([]string{approved}))
	if err := p.Validate(req); err != nil {
		t.Fatalf("registered source: %v", err)
	}
	for _, path := range []string{"", approved + "-sibling", filepath.Join(approved, "child"), "relative", approved + "/../approved"} {
		bad := req
		bad.LocalPath = path
		if err := p.Validate(bad); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	req.Revision = ""
	if err := p.Validate(req); err == nil {
		t.Fatal("missing revision accepted")
	}
}

func TestPrepareRechecksLocalRegistrationBeforeHostRead(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	approved := filepath.Join(dir, "approved")
	other := filepath.Join(dir, "other")
	if err := os.Mkdir(approved, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	p := New(nil, WithLocalPaths([]string{approved}))
	p.runHostCommand = func(context.Context, string, ...string) (string, error) {
		t.Fatal("host git ran before policy rejection")
		return "", nil
	}
	req := Request{Kind: SourceLocal, LocalPath: approved, Revision: "abc123"}
	if err := p.Validate(req); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(approved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, approved); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Prepare(context.Background(), nil, req); !errors.Is(err, ErrRepositoryRejected) {
		t.Fatalf("redirected source: %v", err)
	}
	req.LocalPath = other
	if _, err := p.Prepare(context.Background(), nil, req); !errors.Is(err, ErrRepositoryRejected) {
		t.Fatalf("unregistered direct Prepare: %v", err)
	}
}

func TestUnknownKindRejected(t *testing.T) {
	t.Parallel()
	p := New(nil)
	if err := p.Validate(Request{Kind: "carrier-pigeon", Revision: "main"}); err == nil {
		t.Fatal("expected an unknown source kind to be rejected")
	}
}

func TestValidateRevisionBounds(t *testing.T) {
	t.Parallel()
	if err := ValidateRevision(strings.Repeat("a", 257)); err == nil {
		t.Fatal("expected an over-long revision to be rejected")
	}
	if err := ValidateRevision("a1b2c3"); err != nil {
		t.Fatalf("a normal revision should be accepted: %v", err)
	}
}

func TestRevisionArgsEscapesOptionLikeRevisions(t *testing.T) {
	t.Parallel()
	got := revisionArgs("--upload-pack=evil")
	if len(got) != 2 || got[0] != "--end-of-options" {
		t.Fatalf("revisionArgs = %v, want --end-of-options first", got)
	}
	got = revisionArgs("main")
	if len(got) != 1 || got[0] != "main^{commit}" {
		t.Fatalf("revisionArgs = %v", got)
	}
}

func TestHashTaskIsStable(t *testing.T) {
	t.Parallel()
	a := HashTask("change the greeting")
	b := HashTask("change the greeting")
	c := HashTask("change the greeting!")
	if a != b {
		t.Fatal("HashTask is not deterministic")
	}
	if a == c {
		t.Fatal("HashTask does not distinguish different tasks")
	}
	if len(a) != 64 {
		t.Fatalf("HashTask length = %d, want 64 hex characters", len(a))
	}
}
