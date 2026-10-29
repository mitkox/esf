package secretfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := Read(path, 6)
	if err != nil || string(data) != "secret" {
		t.Fatalf("read=%q err=%v", data, err)
	}
	if _, err := Read(path, 5); err == nil {
		t.Fatal("oversized secret accepted")
	}
	if _, err := Read(path, 0); err == nil {
		t.Fatal("invalid limit accepted")
	}
	for _, mode := range []os.FileMode{0644, 0640, 0620} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path, 6); err == nil {
			t.Fatalf("unsafe mode %o accepted", mode)
		}
	}
}

func TestReadRejectsSymlinkSpecialAndEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, fifo, dir, empty} {
		if _, err := Read(path, 100); err == nil {
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
}
