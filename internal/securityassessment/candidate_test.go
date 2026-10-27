package securityassessment

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/sandbox"
)

// The test adapter executes only when this suite is run inside its Cube VM.
type localCandidateSandbox struct{}

func (localCandidateSandbox) ID() string                    { return "local-cube-test" }
func (localCandidateSandbox) Template() string              { return "test" }
func (localCandidateSandbox) Destroy(context.Context) error { return nil }
func (localCandidateSandbox) WriteFile(_ context.Context, name string, data []byte) error {
	return os.WriteFile(name, data, 0600)
}
func (localCandidateSandbox) ReadFile(_ context.Context, name string) ([]byte, error) {
	return os.ReadFile(name)
}
func (localCandidateSandbox) ReadFileBounded(_ context.Context, name string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("candidate file exceeds transfer limit")
	}
	return data, nil
}
func (localCandidateSandbox) Execute(ctx context.Context, command sandbox.Command) (sandbox.Execution, error) {
	cmd := exec.CommandContext(ctx, command.Argv[0], command.Argv[1:]...)
	cmd.Dir = command.Dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return sandbox.Execution{ExitCode: exit.ExitCode(), Stdout: stdout.String(), Stderr: stderr.String()}, nil
	}
	return sandbox.Execution{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

func TestExportCandidateFreezesFilesystemAndWithholdsCredentials(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"app.py": "dirty", "untracked.txt": "new", ".env": "TOKEN=source-canary"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside-secret")
	if err := os.WriteFile(outside, []byte("outside-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	first, err := ExportCandidate(context.Background(), localCandidateSandbox{}, root, DefaultCandidatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Files["app.py"]) != "dirty" || string(first.Files["untracked.txt"]) != "new" {
		t.Fatal("dirty/untracked source omitted")
	}
	if bytes.Contains(first.Archive, []byte("source-canary")) || bytes.Contains(first.Archive, []byte("outside-canary")) {
		t.Fatal("credential or outside symlink content exported")
	}
	second, err := ExportCandidate(context.Background(), localCandidateSandbox{}, root, DefaultCandidatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCandidateUnchanged(first.Manifest, second.Manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	third, err := ExportCandidate(context.Background(), localCandidateSandbox{}, root, DefaultCandidatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if VerifyCandidateUnchanged(first.Manifest, third.Manifest) == nil {
		t.Fatal("dirty candidate mutation not detected")
	}
}

type growingCandidateSandbox struct {
	localCandidateSandbox
	targetSuffix string
	paths        []string
	limits       []int64
}

func (s *growingCandidateSandbox) ReadFile(context.Context, string) ([]byte, error) {
	panic("candidate export used unbounded ReadFile")
}

func (s *growingCandidateSandbox) ReadFileBounded(ctx context.Context, name string, maxBytes int64) ([]byte, error) {
	s.paths = append(s.paths, name)
	s.limits = append(s.limits, maxBytes)
	if strings.HasSuffix(name, s.targetSuffix) {
		// Simulate the builder replacing a correctly inventoried artifact before
		// the host transfer begins. Bounds must apply to the new stream.
		if err := os.WriteFile(name, bytes.Repeat([]byte{'x'}, int(maxBytes+1)), 0600); err != nil {
			return nil, err
		}
	}
	return s.localCandidateSandbox.ReadFileBounded(ctx, name, maxBytes)
}

func TestExportCandidateRequiresBoundedTransferBeforeSandboxActions(t *testing.T) {
	// A nil embedded interface would panic on any sandbox operation. A provider
	// without this capability must be rejected before even cleanup is scheduled.
	legacy := struct{ sandbox.Sandbox }{}
	if _, err := ExportCandidate(context.Background(), legacy, "/workspace/repository", DefaultCandidatePolicy()); err == nil {
		t.Fatal("candidate export accepted unbounded provider")
	}
}

func TestExportCandidateRejectsGrowingArchiveAndInventoryAndCleansBoth(t *testing.T) {
	for _, suffix := range []string{".tar", ".json"} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("safe"), 0644); err != nil {
				t.Fatal(err)
			}
			sb := &growingCandidateSandbox{targetSuffix: suffix}
			policy := CandidatePolicy{MaxFiles: 1, MaxBytes: 4, MaxFileBytes: 4}
			if _, err := ExportCandidate(context.Background(), sb, root, policy); err == nil {
				t.Fatal("candidate export accepted replaced oversized file")
			}
			if len(sb.paths) == 0 || sb.limits[0] != maxCandidateInventoryBytes {
				t.Fatal("candidate inventory was not read through its transport bound")
			}
			if suffix == ".tar" && (len(sb.limits) != 2 || sb.limits[1] != policy.archiveLimit()) {
				t.Fatal("archive transport bound was not derived from candidate policy")
			}
			inventoryPath := sb.paths[0]
			for _, name := range []string{inventoryPath, strings.TrimSuffix(inventoryPath, ".json")} {
				if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("private candidate artifact was not cleaned: %v", err)
				}
			}
		})
	}
}

func candidateArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var output bytes.Buffer
	w := tar.NewWriter(&output)
	for _, name := range []string{"app.py", "untracked.txt", "../escape"} {
		data, ok := entries[name]
		if !ok {
			continue
		}
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestCandidateIncludesDirtyAndUntrackedBytes(t *testing.T) {
	archive := candidateArchive(t, map[string]string{"app.py": "dirty", "untracked.txt": "new"})
	inventory := CandidateManifest{Schema: CandidateSchema, Entries: []CandidateEntry{
		{Path: "app.py", Kind: "file", Mode: 0644, Size: 5, SHA256: SHA256([]byte("dirty"))},
		{Path: "untracked.txt", Kind: "file", Mode: 0644, Size: 3, SHA256: SHA256([]byte("new"))},
	}}
	got, err := candidateFromArchive(inventory, archive, DefaultCandidatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Files["app.py"]) != "dirty" || string(got.Files["untracked.txt"]) != "new" {
		t.Fatal("candidate bytes lost")
	}
	changed := got.Manifest
	changed.Entries = append([]CandidateEntry(nil), got.Manifest.Entries...)
	changed.Entries[0].SHA256 = SHA256([]byte("other"))
	changed.Digest = candidateDigest(changed)
	if VerifyCandidateUnchanged(got.Manifest, changed) == nil {
		t.Fatal("candidate drift accepted")
	}
}

func TestCandidateRejectsTraversalSizeAndHashMismatch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest CandidateManifest
		data     []byte
		policy   CandidatePolicy
	}{
		{"traversal", CandidateManifest{Schema: CandidateSchema, Entries: []CandidateEntry{{Path: "../escape", Kind: "file", Size: 1, Mode: 0644, SHA256: SHA256([]byte("x"))}}}, candidateArchive(t, map[string]string{"../escape": "x"}), DefaultCandidatePolicy()},
		{"wrong-bytes", CandidateManifest{Schema: CandidateSchema, Entries: []CandidateEntry{{Path: "app.py", Kind: "file", Size: 1, Mode: 0644, SHA256: SHA256([]byte("x"))}}}, candidateArchive(t, map[string]string{"app.py": "y"}), DefaultCandidatePolicy()},
		{"oversized", CandidateManifest{Schema: CandidateSchema, Entries: []CandidateEntry{{Path: "app.py", Kind: "file", Size: 4, Mode: 0644, SHA256: SHA256([]byte("four"))}}}, candidateArchive(t, map[string]string{"app.py": "four"}), CandidatePolicy{MaxFiles: 1, MaxBytes: 3, MaxFileBytes: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := candidateFromArchive(tc.manifest, tc.data, tc.policy); err == nil {
				t.Fatal("unsafe candidate accepted")
			}
		})
	}
}

func TestExcludedCredentialAndSymlinkAreVisibleButNeverExported(t *testing.T) {
	inventory := CandidateManifest{Schema: CandidateSchema, Entries: []CandidateEntry{
		{Path: ".env", Kind: "file", Mode: 0600, Size: 11, SHA256: SHA256([]byte("secret-data")), Excluded: "credential-path"},
		{Path: "outside", Kind: "symlink", Mode: 0777, Size: 4, SHA256: SHA256([]byte("/etc")), Excluded: "symlink"},
	}}
	got, err := candidateFromArchive(inventory, candidateArchive(t, nil), DefaultCandidatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 0 || len(got.Manifest.Entries) != 2 || got.Manifest.Digest == "" {
		t.Fatal("exclusions were hidden or exported")
	}
	if bytes.Contains(got.Archive, []byte("secret-data")) {
		t.Fatal("credential data exported")
	}
}
