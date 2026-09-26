//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
)

func checkSQLiteFilesystem(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect storage.data_dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("storage.data_dir is not a directory")
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return fmt.Errorf("inspect storage filesystem: %w", err)
	}
	if !supportedSQLiteFSType(uint64(stat.Type)) {
		return fmt.Errorf("SQLite storage requires a qualified local or block-backed filesystem (type %#x)", uint64(stat.Type))
	}
	return nil
}

func supportedSQLiteFSType(value uint64) bool {
	switch value {
	case 0xef53, // ext2/3/4
		0x58465342, // XFS
		0x9123683e, // btrfs
		0xf2f52010, // F2FS
		0x2fc12fc1: // ZFS
		return true
	default:
		return false
	}
}
