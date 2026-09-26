package artifacts

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) (*Local, Store) {
	t.Helper()
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	store, err := local.ForRun("run-test")
	if err != nil {
		t.Fatalf("ForRun: %v", err)
	}
	return local, store
}

func TestArtifactWriteLimitRejectsBeforeCreatingFile(t *testing.T) {
	t.Parallel()
	local, err := NewLocalWithLimit(t.TempDir(), 2048)
	if err != nil {
		t.Fatal(err)
	}
	store, err := local.ForRun("bounded")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("oversized.log", bytes.Repeat([]byte("x"), 2049)); !errors.Is(err, ErrArtifactTooLarge) {
		t.Fatalf("Write error = %v", err)
	}
	if exists, err := store.Exists("oversized.log"); err != nil || exists {
		t.Fatalf("oversized file exists: %t, %v", exists, err)
	}
	if err := store.Write("allowed.log", bytes.Repeat([]byte("x"), 2048)); err != nil {
		t.Fatal(err)
	}
}

func TestWriteReadExistsList(t *testing.T) {
	t.Parallel()
	_, store := newStore(t)

	if err := store.Write("agent/stdout.log", []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := store.Read("agent/stdout.log")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("Read = %q", data)
	}

	ok, err := store.Exists("agent/stdout.log")
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v; want true", ok, err)
	}
	ok, err = store.Exists("agent/missing.log")
	if err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v; want false", ok, err)
	}

	if err := store.Write("run.json", []byte("{}")); err != nil {
		t.Fatalf("Write run.json: %v", err)
	}
	paths, err := store.List(".")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("List returned %v, want 2 entries", paths)
	}
}

// TestPathTraversalIsRejected is a security test: a run ID or step ID must never
// be able to write outside its own run directory.
func TestPathTraversalIsRejected(t *testing.T) {
	t.Parallel()
	_, store := newStore(t)

	badPaths := []string{
		"../escape.txt",
		"../../etc/passwd",
		"a/../../escape.txt",
		"/etc/passwd",
		"",
		".",
		"..",
	}
	for _, p := range badPaths {
		p := p
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			if err := store.Write(p, []byte("x")); err == nil {
				t.Fatalf("expected Write(%q) to be rejected", p)
			}
		})
	}
}

func TestRunIDTraversalIsRejected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	local, err := NewLocal(root)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	for _, runID := range []string{"../escape", "a/b", ".", "..", "", `a\b`} {
		if _, err := local.ForRun(runID); err == nil {
			t.Fatalf("expected run id %q to be rejected", runID)
		}
	}
}

// TestArtifactsSurviveSandboxDestruction documents the property that makes the
// store useful: evidence lives on the factory host, never only in a microVM.
func TestArtifactsSurviveSandboxDestruction(t *testing.T) {
	t.Parallel()
	local, store := newStore(t)
	if err := store.Write("changes.patch", []byte("diff --git a/x b/x")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Simulate re-opening after the run (a new process, a restarted worker).
	reopened, err := local.ForRun("run-test")
	if err != nil {
		t.Fatalf("ForRun (reopen): %v", err)
	}
	data, err := reopened.Read("changes.patch")
	if err != nil {
		t.Fatalf("Read after reopen: %v", err)
	}
	if !strings.Contains(string(data), "diff --git") {
		t.Fatalf("artifact did not survive: %q", data)
	}
}

func TestForRunIsIdempotentAndNonDestructive(t *testing.T) {
	t.Parallel()
	local, store := newStore(t)
	if err := store.Write("run.json", []byte("first")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Re-opening must not erase evidence: an activity retry must never destroy
	// the record of an earlier attempt.
	again, err := local.ForRun("run-test")
	if err != nil {
		t.Fatalf("ForRun: %v", err)
	}
	data, err := again.Read("run.json")
	if err != nil || string(data) != "first" {
		t.Fatalf("re-opening the run store destroyed evidence: %q %v", data, err)
	}
}

func TestArtifactPermissionsAreNotWorldReadable(t *testing.T) {
	t.Parallel()
	local, store := newStore(t)
	if err := store.Write("run.json", []byte("{}")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	path := filepath.Join(local.Root(), "runs", "run-test", "run.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("artifact is world-accessible: %v", info.Mode().Perm())
	}
}

func TestNewLocalRequiresRoot(t *testing.T) {
	t.Parallel()
	if _, err := NewLocal("  "); err == nil {
		t.Fatal("expected an empty root to be rejected")
	}
}

func TestReadMissingArtifact(t *testing.T) {
	t.Parallel()
	_, store := newStore(t)
	if _, err := store.Read("nope.json"); err == nil {
		t.Fatal("expected an error reading a missing artifact")
	} else if errors.Is(err, ErrInvalidPath) {
		t.Fatalf("a missing file is not an invalid path: %v", err)
	}
}

func TestConcurrentReadersSeeCompleteArtifacts(t *testing.T) {
	_, store := newStore(t)
	first := bytes.Repeat([]byte("a"), 128*1024)
	second := bytes.Repeat([]byte("b"), 128*1024)
	if err := store.Write("run.json", first); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 50; i++ {
			data := first
			if i%2 == 0 {
				data = second
			}
			if err := store.Write("run.json", data); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for {
		data, err := store.Read("run.json")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, first) && !bytes.Equal(data, second) {
			t.Fatal("reader saw a partial artifact")
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			paths, err := store.List("")
			if err != nil || len(paths) != 1 {
				t.Fatalf("temporary files leaked: %v, %v", paths, err)
			}
			return
		default:
		}
	}
}
