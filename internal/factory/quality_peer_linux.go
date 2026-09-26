//go:build linux

package factory

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

func qualityPeerUID(conn net.Conn) (uint32, error) {
	u, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("Unix peer required")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var inner error
	err = raw.Control(func(fd uintptr) {
		cred, inner = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return 0, err
	}
	if inner != nil {
		return 0, inner
	}
	return cred.Uid, nil
}

func qualityCheckDirectory(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || stat.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("quality directory must be owned by the worker and not writable by other users: %s", path)
	}
	return nil
}
