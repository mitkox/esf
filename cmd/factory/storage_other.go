//go:build !linux

package main

import (
	"fmt"
	"os"
)

func checkSQLiteFilesystem(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect storage.data_dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("storage.data_dir is not a directory")
	}
	return nil
}
