// Package secretfile reads bounded, private credentials from operator-owned files.
package secretfile

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// Read refuses symlinks, special files, and group/other access. Validation uses
// the opened descriptor so replacing the pathname cannot bypass the checks.
func Read(path string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("secret file size limit must be positive")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("secret file must be a regular file, not a symlink or device")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, fmt.Errorf("secret file changed while opening or is not regular")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secret file permissions %04o are readable outside its owner; chmod 600 it", info.Mode().Perm())
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("secret file exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > limit {
		return nil, fmt.Errorf("secret file is empty or exceeds %d bytes", limit)
	}
	return data, nil
}
