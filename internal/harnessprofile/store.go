package harnessprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Store uses rooted file operations and exclusive, content-addressed writes.
// A shared durable filesystem is required for multi-worker deployments.
type Store struct{ root *os.Root }

// Qualification receipts retain the raw repeated trials, separately from the
// much smaller instructional bundle limit.
const MaxArtifactBytes = 4 << 20

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Store{root: r}, nil
}
func (s *Store) Close() error { return s.root.Close() }
func (s *Store) Put(p Profile) (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	if p.Parent != "" {
		parent, e := s.Get(p.Parent)
		if e != nil {
			return "", e
		}
		if parent.Binding != p.Binding {
			return "", errors.New("profile parent belongs to a different binding")
		}
	}
	d := p.Digest()
	b, _ := json.Marshal(p)
	if err := s.WriteImmutable("objects/"+d[7:]+".json", b); err != nil {
		return "", err
	}
	return d, nil
}
func (s *Store) Get(d string) (Profile, error) {
	if !DigestPattern.MatchString(d) {
		return Profile{}, errors.New("invalid profile digest")
	}
	b, err := s.Read("objects/"+d[7:]+".json", MaxBytes*2)
	if err != nil {
		return Profile{}, err
	}
	p, err := Decode(bytes.NewReader(b))
	if err != nil {
		return p, err
	}
	if p.Digest() != d {
		return p, errors.New("profile content digest mismatch")
	}
	return p, nil
}
func (s *Store) Read(path string, limit int64) ([]byte, error) {
	if e := s.noLinks(path); e != nil {
		return nil, e
	}
	f, err := s.root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil || !i.Mode().IsRegular() {
		return nil, errors.New("not a regular profile artifact")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errors.New("profile artifact too large")
	}
	return b, err
}
func (s *Store) WriteImmutable(path string, b []byte) error {
	if e := s.noLinks(path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if len(b) > MaxArtifactBytes {
		return errors.New("profile artifact too large")
	}
	if err := s.root.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock, err := s.root.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if _, err := s.root.Lstat(path); err == nil {
		old, e := s.Read(path, MaxArtifactBytes)
		if e != nil {
			return e
		}
		if !bytes.Equal(old, b) {
			return fmt.Errorf("immutable artifact conflict: %s", path)
		}
		return nil
	}
	f, err := s.root.OpenFile(path+".pending", os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = s.root.Rename(path+".pending", path); err != nil {
		return err
	}
	d, err := s.root.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *Store) noLinks(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") || filepath.Clean(path) != path {
		return errors.New("invalid profile artifact path")
	}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == ".." || part == "." {
			return errors.New("invalid profile artifact path")
		}
		info, e := s.root.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("profile artifacts cannot contain symlinks")
		}
	}
	return nil
}
