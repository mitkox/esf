package factory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lockedState serializes read-modify-write updates of one JSON state file.
//
// The lock file is opened through the root without following a symlink, and the
// state file is replaced atomically with an owner-only mode, so a concurrent
// reader sees either the previous or the next complete document.
type lockedState[T any] struct {
	root        string
	fileName    string
	lockName    string
	tempPattern string
	label       string
	newState    func() T
	normalize   func(*T)
}

func (s *lockedState[T]) load() (T, error) {
	state := s.newState()
	data, err := os.ReadFile(filepath.Join(s.root, s.fileName))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("%s: decode state: %w", s.label, err)
	}
	s.normalize(&state)
	return state, nil
}

func (s *lockedState[T]) withState(update func(*T) error) error {
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return err
	}
	defer root.Close()
	lock, err := root.OpenFile(s.lockName, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	state, err := s.load()
	if err != nil {
		return err
	}
	if err := update(&state); err != nil {
		return err
	}
	return s.save(state)
}

func (s *lockedState[T]) save(state T) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: encode state: %w", s.label, err)
	}
	tmp, err := os.CreateTemp(s.root, s.tempPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(s.root, s.fileName)); err != nil {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
