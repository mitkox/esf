package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type storageUsage struct {
	Root       string `json:"root"`
	Bytes      int64  `json:"bytes"`
	RunsBytes  int64  `json:"runs_bytes"`
	OtherBytes int64  `json:"other_bytes"`
	Files      int64  `json:"files"`
	Runs       int64  `json:"runs"`
}

func newStorageCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{Use: "storage", Short: "Inspect factory storage"}
	var output string
	usage := &cobra.Command{Use: "usage", Short: "Show durable factory storage usage", RunE: func(cmd *cobra.Command, _ []string) error {
		if output != "" && output != "json" {
			return fmt.Errorf("unsupported output %q", output)
		}
		cfg, err := loadConfig(*configPath)
		if err != nil {
			return err
		}
		root, err := filepath.Abs(cfg.Storage.DataDir)
		if err != nil {
			return err
		}
		result := storageUsage{Root: root}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if filepath.Dir(path) == filepath.Join(root, "runs") {
					result.Runs++
				}
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			result.Bytes += info.Size()
			result.Files++
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if strings.HasPrefix(rel, "runs"+string(os.PathSeparator)) {
				result.RunsBytes += info.Size()
			} else {
				result.OtherBytes += info.Size()
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("inspect factory storage: %w", err)
		}
		if output == "json" {
			return printJSON(result)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %d bytes (%d runs, %d files; %d run bytes, %d other bytes)\n", result.Root, result.Bytes, result.Runs, result.Files, result.RunsBytes, result.OtherBytes)
		return err
	}}
	usage.Flags().StringVar(&output, "output", "", "output format: json")
	cmd.AddCommand(usage)
	return cmd
}
