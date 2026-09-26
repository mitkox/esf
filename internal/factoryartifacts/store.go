// Package artifacts provides durable storage for factory evidence.
//
// Design constraints:
//
//   - Artifacts must survive sandbox destruction. They are never written only
//     inside a microVM.
//   - The store is intentionally domain-free: it stores bytes at validated
//     relative paths and knows nothing about runs, manifests, or states. That
//     keeps a future S3/MinIO backend a drop-in replacement.
//   - Every path is validated against traversal, so a run ID or step ID can
//     never escape the store root.
package artifacts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrInvalidPath is returned for a path that would escape the store root or is
// otherwise unusable.
var ErrInvalidPath = errors.New("artifacts: invalid path")
var ErrArtifactTooLarge = errors.New("artifacts: artifact exceeds configured size limit")

const DefaultMaxArtifactBytes int64 = 32 << 20

// Store is a durable, per-run artifact store.
//
// Write satisfies verification.Sink structurally, so the verification runner
// can persist output without importing this package.
type Store interface {
	// Write stores data at a store-relative path, creating parents.
	Write(relPath string, data []byte) error
	// Read returns the contents of a stored artifact.
	Read(relPath string) ([]byte, error)
	// Exists reports whether an artifact is present.
	Exists(relPath string) (bool, error)
	// List returns every stored path under prefix, sorted.
	List(prefix string) ([]string, error)
	// Location identifies where artifacts live, for evidence.
	Location() string
	// Dir is the absolute directory holding this run's artifacts.
	Dir() string
}

// Factory opens per-run stores.
type Factory interface {
	// ForRun returns the store for a run. Opening is idempotent and does not
	// erase existing artifacts, so a retried activity cannot destroy evidence.
	ForRun(runID string) (Store, error)
	// Root identifies the storage root, for evidence.
	Root() string
}

// Local is a filesystem-backed artifact factory.
//
// The layout is deliberately flat and predictable:
//
//	<root>/runs/<run-id>/...
type Local struct {
	root             string
	maxArtifactBytes int64
}

// NewLocal creates a filesystem artifact factory rooted at root.
func NewLocal(root string) (*Local, error) {
	return NewLocalWithLimit(root, DefaultMaxArtifactBytes)
}

// NewLocalWithLimit sets the maximum size of one durable artifact.
func NewLocalWithLimit(root string, maxArtifactBytes int64) (*Local, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("artifacts: root is required")
	}
	if maxArtifactBytes <= 0 {
		return nil, fmt.Errorf("artifacts: maximum artifact size must be positive")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("artifacts: resolve root %q: %w", root, err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("artifacts: create root %q: %w", abs, err)
	}
	return &Local{root: abs, maxArtifactBytes: maxArtifactBytes}, nil
}

// Root returns the storage root.
func (l *Local) Root() string { return l.root }

// ForRun returns the store for runID.
func (l *Local) ForRun(runID string) (Store, error) {
	if err := validateSegment(runID); err != nil {
		return nil, fmt.Errorf("artifacts: invalid run id %q: %w", runID, err)
	}
	dir := filepath.Join(l.root, "runs", runID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("artifacts: create run directory %q: %w", dir, err)
	}
	return &RunStore{dir: dir, location: filepath.ToSlash(filepath.Join("runs", runID)), maxArtifactBytes: l.maxArtifactBytes}, nil
}

// RunStore is the artifact store for one run.
type RunStore struct {
	dir              string
	location         string
	maxArtifactBytes int64
}

// Location identifies the run's artifact directory.
func (s *RunStore) Location() string { return s.location }

// Dir returns the absolute directory holding this run's artifacts.
func (s *RunStore) Dir() string { return s.dir }

// Write stores data at a path relative to the run directory.
func (s *RunStore) Write(relPath string, data []byte) error {
	if int64(len(data)) > s.maxArtifactBytes {
		return fmt.Errorf("%w: %s is %d bytes (limit %d)", ErrArtifactTooLarge, relPath, len(data), s.maxArtifactBytes)
	}
	full, err := s.resolve(relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return fmt.Errorf("artifacts: create directory for %q: %w", relPath, err)
	}
	// Publish complete files atomically. Readers must never observe a
	// truncated manifest while an activity retry replaces it.
	tmp, err := os.CreateTemp(filepath.Dir(full), ".artifact-*")
	if err != nil {
		return fmt.Errorf("artifacts: stage %q: %w", relPath, err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if err := tmp.Chmod(0o640); err != nil {
		return fmt.Errorf("artifacts: chmod %q: %w", relPath, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("artifacts: write %q: %w", relPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("artifacts: sync %q: %w", relPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("artifacts: close %q: %w", relPath, err)
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		return fmt.Errorf("artifacts: publish %q: %w", relPath, err)
	}
	dir, err := os.Open(filepath.Dir(full))
	if err != nil {
		return fmt.Errorf("artifacts: open parent of %q: %w", relPath, err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("artifacts: sync parent of %q: %w", relPath, err)
	}
	return nil
}

// Read returns a stored artifact.
func (s *RunStore) Read(relPath string) ([]byte, error) {
	full, err := s.resolve(relPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("artifacts: read %q: %w", relPath, err)
	}
	return data, nil
}

// Exists reports whether an artifact is stored.
func (s *RunStore) Exists(relPath string) (bool, error) {
	full, err := s.resolve(relPath)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(full)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("artifacts: stat %q: %w", relPath, err)
	}
}

// List returns every stored file under prefix, as store-relative paths.
func (s *RunStore) List(prefix string) ([]string, error) {
	base := s.dir
	if strings.TrimSpace(prefix) != "" && prefix != "." && prefix != "/" {
		resolved, err := s.resolve(prefix)
		if err != nil {
			return nil, err
		}
		base = resolved
	}
	var paths []string
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.dir, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("artifacts: list %q: %w", prefix, err)
	}
	sort.Strings(paths)
	return paths, nil
}

// resolve validates a store-relative path and returns its absolute form.
//
// Rejecting traversal here is what makes run IDs and step IDs safe to use as
// path components.
func (s *RunStore) resolve(relPath string) (string, error) {
	if strings.TrimSpace(relPath) == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "/") {
		return "", fmt.Errorf("%w: %q must be relative", ErrInvalidPath, relPath)
	}
	cleaned := filepath.Clean(filepath.FromSlash(relPath))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q escapes the run directory", ErrInvalidPath, relPath)
	}
	full := filepath.Join(s.dir, cleaned)
	// Defence in depth: confirm the joined path is still inside the run dir.
	rel, err := filepath.Rel(s.dir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q escapes the run directory", ErrInvalidPath, relPath)
	}
	return full, nil
}

// validateSegment rejects a path segment that could escape its parent.
func validateSegment(segment string) error {
	if strings.TrimSpace(segment) == "" {
		return fmt.Errorf("%w: empty segment", ErrInvalidPath)
	}
	if strings.ContainsAny(segment, `/\`) || segment == "." || segment == ".." {
		return fmt.Errorf("%w: %q is not a plain path segment", ErrInvalidPath, segment)
	}
	return nil
}

var (
	_ Factory = (*Local)(nil)
	_ Store   = (*RunStore)(nil)
)
