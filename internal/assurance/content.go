package assurance

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxObjectBytes = 256 << 20

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Content is worker-private immutable storage. Names are derived exclusively
// from digests; caller-controlled paths never reach filesystem operations.
type Content struct{ root string }

func OpenContent(root string) (*Content, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("content root must be a real directory")
	}
	if err = os.Chmod(root, 0700); err != nil {
		return nil, err
	}
	return &Content{root}, nil
}
func (c *Content) path(digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", fmt.Errorf("invalid content digest")
	}
	return filepath.Join(c.root, strings.TrimPrefix(digest, "sha256:")), nil
}
func (c *Content) Put(data []byte) (string, error) {
	if len(data) > MaxObjectBytes {
		return "", fmt.Errorf("object exceeds %d bytes", MaxObjectBytes)
	}
	digest := Digest(data)
	dest, _ := c.path(digest)
	if old, err := c.Read(digest); err == nil {
		if !bytes.Equal(old, data) {
			return "", ErrConflict
		}
		return digest, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	f, err := os.CreateTemp(c.root, ".pending-")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return "", err
	}
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	// Link is no-replace; concurrent publication must compare the existing bytes.
	if err = os.Link(f.Name(), dest); err != nil {
		if !os.IsExist(err) {
			return "", err
		}
		old, e := c.Read(digest)
		if e != nil || !bytes.Equal(old, data) {
			return "", ErrConflict
		}
	}
	d, err := os.Open(c.root)
	if err != nil {
		return "", err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return "", err
	}
	return digest, nil
}
func (c *Content) Read(digest string) ([]byte, error) {
	p, err := c.path(digest)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxObjectBytes {
		return nil, fmt.Errorf("invalid content object")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxObjectBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxObjectBytes || Digest(b) != digest {
		return nil, fmt.Errorf("%w: content digest mismatch", ErrConflict)
	}
	return b, nil
}
func (c *Content) Verify(e Evidence) error {
	b, err := c.Read(e.Digest)
	if err != nil {
		return err
	}
	if int64(len(b)) != e.Size {
		return fmt.Errorf("evidence size mismatch")
	}
	return nil
}

// VerifiedPath is for trusted host consumers such as harness provisioning.
func (c *Content) VerifiedPath(digest string) (string, error) {
	if _, err := c.Read(digest); err != nil {
		return "", err
	}
	return c.path(digest)
}
