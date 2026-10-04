package artifacts

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemBoundedImmutablePublication(t *testing.T) {
	store, err := NewFilesystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Stage(strings.NewReader("12345"), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	p, err := store.Stage(strings.NewReader("hello"), 5)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Size != 5 || p.Checksum != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatal(p)
	}
	if err = store.Publish(p, "task/run/file"); err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(p, "task/run/file"); err != nil {
		t.Fatal(err)
	}
	f, err := store.Open("task/run/file")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
	other, err := store.Stage(strings.NewReader("other"), 5)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := store.Publish(other, "task/run/file"); err == nil {
		t.Fatal("accepted conflicting immutable publication")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(store.directory, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(p, "escape/file"); err == nil {
		t.Fatal("published through a symlink outside the store")
	}
	if _, err := os.Stat(filepath.Join(outside, "file")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external file was created: %v", err)
	}
	if err := os.Rename(filepath.Join(store.directory, ".uploads"), filepath.Join(store.directory, ".uploads-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store.directory, ".uploads")); err != nil {
		t.Fatal(err)
	}
	if staged, err := store.Stage(strings.NewReader("escape"), 6); err == nil {
		staged.Close()
		t.Fatal("staged through a symlink outside the store")
	}
	for _, path := range []string{"../secret", "/tmp/file", "a/../b", "a\\b", "."} {
		if ValidPath(path) {
			t.Fatalf("accepted %s", path)
		}
	}
}
