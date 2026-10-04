package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Projected Kubernetes Secrets use symlinks and fsGroup permissions. Copy them
// into a private, memory-backed directory before starting the factory, whose
// credential readers intentionally require regular owner-only files.
func newCredentialsCommand() *cobra.Command {
	var source, destination string
	stage := &cobra.Command{Use: "stage", Short: "Stage projected credentials as private regular files", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return stageCredentials(source, destination) }}
	stage.Flags().StringVar(&source, "source", "", "read-only projected Secret directory")
	stage.Flags().StringVar(&destination, "destination", "", "empty private destination directory")
	command := &cobra.Command{Use: "credentials", Short: "Prepare operator credential files"}
	command.AddCommand(stage)
	return command
}

func stageCredentials(source, destination string) (err error) {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || filepath.Clean(source) == filepath.Clean(destination) {
		return errors.New("credential directories must be distinct absolute paths")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return err
	}
	// A fresh directory also prevents old credentials surviving a restart.
	parent, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer parent.Close()
	name := filepath.Base(destination)
	if err := parent.Mkdir(name, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = parent.RemoveAll(name)
		}
	}()
	dest, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer dest.Close()
	total, count := int64(0), 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		} // projected-volume metadata
		input, err := root.Open(name)
		if err != nil {
			return fmt.Errorf("open projected credential: %w", err)
		}
		info, err := input.Stat()
		if err != nil || !info.Mode().IsRegular() {
			input.Close()
			return errors.New("projected credential must resolve to a regular file")
		}
		count++
		if count > 128 || info.Size() > 64<<10 || info.Size() > (1<<20)-total {
			input.Close()
			return errors.New("projected credential limit exceeded")
		}
		output, err := dest.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			return err
		}
		n, copyErr := io.Copy(output, io.LimitReader(input, (64<<10)+1))
		input.Close()
		syncErr := output.Sync()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		total += n
		if n > 64<<10 || total > 1<<20 {
			return errors.New("projected credential limit exceeded")
		}
	}
	if count == 0 {
		return errors.New("projected credential directory is empty")
	}
	return nil
}
