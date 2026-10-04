// Package artifacts stores immutable execution outputs independently of workers.
package artifacts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrTooLarge = errors.New("artifact size limit exceeded")

type Store interface {
	Stage(io.Reader, int64) (*Pending, error)
	Publish(*Pending, string) error
	Open(string) (*os.File, error)
	Delete(string) error
}
type Filesystem struct{ directory string }
type Pending struct {
	Path, Checksum string
	Size           int64
	root           *os.Root
	relative       string
}

func (p *Pending) Open() (*os.File, error) {
	if p.root != nil {
		return p.root.Open(p.relative)
	}
	return os.Open(p.Path)
}
func (p *Pending) Close() {
	if p.root != nil {
		_ = p.root.Remove(p.relative)
		_ = p.root.Close()
		return
	}
	_ = os.Remove(p.Path)
}
func ValidPath(value string) bool {
	return value != "" && len(value) <= 1024 && value != "." && !strings.ContainsAny(value, "\\\x00\r\n") && !strings.HasPrefix(value, "/") && path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../")
}
func NewFilesystem(directory string) (*Filesystem, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(abs, ".uploads"), 0700); err != nil {
		return nil, err
	}
	return &Filesystem{abs}, nil
}
func (s *Filesystem) Stage(reader io.Reader, limit int64) (pending *Pending, err error) {
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return nil, err
	}
	relative := ".uploads/upload-" + rand.Text()
	f, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		root.Close()
		return nil, err
	}
	defer f.Close()
	defer func() {
		if err != nil {
			root.Remove(relative)
			root.Close()
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if n > limit {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, limit)
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	return &Pending{Path: filepath.Join(s.directory, relative), Checksum: hex.EncodeToString(hash.Sum(nil)), Size: n, root: root, relative: relative}, nil
}
func (s *Filesystem) Publish(p *Pending, key string) error {
	if !ValidPath(key) {
		return errors.New("invalid artifact key")
	}
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return err
	}
	defer root.Close()
	staged, err := filepath.Rel(s.directory, p.Path)
	if err != nil || !ValidPath(filepath.ToSlash(staged)) || filepath.Dir(staged) != ".uploads" {
		return errors.New("invalid staged artifact")
	}
	if err := root.MkdirAll(path.Dir(key), 0700); err != nil {
		return err
	}
	if err := root.Link(staged, key); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		// Retrying the same upload is safe; an existing, different artifact is not.
		file, openErr := root.Open(key)
		if openErr != nil {
			return openErr
		}
		hash := sha256.New()
		n, hashErr := io.Copy(hash, io.LimitReader(file, p.Size+1))
		closeErr := file.Close()
		if hashErr != nil {
			return hashErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != p.Size || hex.EncodeToString(hash.Sum(nil)) != p.Checksum {
			return errors.New("artifact key already contains different content")
		}
	}
	// Persist new directory entries all the way to the storage root.
	for parent := path.Dir(key); ; parent = path.Dir(parent) {
		dir, err := root.Open(parent)
		if err != nil {
			return err
		}
		err = dir.Sync()
		closeErr := dir.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if parent == "." {
			break
		}
	}
	return nil
}
func (s *Filesystem) Open(key string) (*os.File, error) {
	if !ValidPath(key) {
		return nil, errors.New("invalid artifact key")
	}
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(key)
}
func (s *Filesystem) Delete(key string) error {
	if !ValidPath(key) {
		return errors.New("invalid artifact key")
	}
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return err
	}
	defer root.Close()
	err = root.Remove(key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
