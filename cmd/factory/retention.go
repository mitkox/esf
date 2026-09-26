package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type orphanArtifact struct {
	path     string
	size     int64
	modified time.Time
}

type retentionPlan struct {
	Root           string   `json:"root"`
	Kind           string   `json:"kind"`
	OlderThanDays  int      `json:"older_than_days"`
	Files          int      `json:"files"`
	Bytes          int64    `json:"bytes"`
	PlanDigest     string   `json:"plan_digest"`
	Paths          []string `json:"paths"`
	PathsTruncated bool     `json:"paths_truncated"`
}

// Only interrupted atomic writes are eligible. Completed manifests, patches,
// logs, changes and QMS records never enter this retention set.
func findOrphanArtifacts(root string, cutoff time.Time) ([]orphanArtifact, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, fmt.Errorf("factory storage root is not a directory")
	}
	runs := filepath.Join(root, "runs")
	if _, err := os.Stat(runs); errors.Is(err, os.ErrNotExist) {
		return []orphanArtifact{}, nil
	} else if err != nil {
		return nil, err
	}
	items := make([]orphanArtifact, 0)
	err = filepath.WalkDir(runs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), ".artifact-") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.ModTime().Before(cutoff) {
			return nil
		}
		items = append(items, orphanArtifact{path: path, size: info.Size(), modified: info.ModTime()})
		if len(items) > 100_000 {
			return fmt.Errorf("too many orphan artifacts; inspect storage before retention")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })
	return items, nil
}

func makeRetentionPlan(root string, days int, items []orphanArtifact) retentionPlan {
	plan := retentionPlan{Root: root, Kind: "orphaned_atomic_write_temp_files", OlderThanDays: days, Files: len(items), Paths: []string{}}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%d\n", root, days)
	for _, item := range items {
		rel, _ := filepath.Rel(root, item.path)
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\n", filepath.ToSlash(rel), item.size, item.modified.UnixNano())
		plan.Bytes += item.size
		if len(plan.Paths) < 100 {
			plan.Paths = append(plan.Paths, filepath.ToSlash(rel))
		}
	}
	plan.PathsTruncated = len(items) > len(plan.Paths)
	plan.PlanDigest = hex.EncodeToString(hash.Sum(nil))
	return plan
}

func applyOrphanArtifacts(items []orphanArtifact) error {
	// Recheck every candidate before deleting any file. An active writer that
	// replaced a path after preview invalidates the plan.
	for _, item := range items {
		info, err := os.Lstat(item.path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != item.size || !info.ModTime().Equal(item.modified) {
			return fmt.Errorf("retention candidate changed: %s", item.path)
		}
	}
	for _, item := range items {
		if err := os.Remove(item.path); err != nil {
			return fmt.Errorf("remove orphan artifact %s: %w", item.path, err)
		}
	}
	return nil
}

func newRetentionCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{Use: "retention", Short: "Preview or remove orphaned temporary artifact files"}
	for _, action := range []string{"preview", "apply"} {
		action := action
		var days int
		var output, digest string
		child := &cobra.Command{Use: action, Short: action + " orphaned atomic-write files", RunE: func(command *cobra.Command, _ []string) error {
			if days < 7 || days > 3650 {
				return fmt.Errorf("older-than-days must be between 7 and 3650")
			}
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
			items, err := findOrphanArtifacts(root, time.Now().AddDate(0, 0, -days))
			if err != nil {
				return err
			}
			plan := makeRetentionPlan(root, days, items)
			if action == "apply" {
				if len(digest) != 64 || !strings.EqualFold(digest, plan.PlanDigest) {
					return fmt.Errorf("retention plan changed or --plan-digest is missing; run preview again")
				}
				if err := applyOrphanArtifacts(items); err != nil {
					return err
				}
			}
			if output == "json" {
				return printJSON(plan)
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "%s: %d orphan temporary files, %d bytes; plan digest %s\n", action, plan.Files, plan.Bytes, plan.PlanDigest)
			return err
		}}
		child.Flags().IntVar(&days, "older-than-days", 30, "minimum age of interrupted atomic writes")
		child.Flags().StringVar(&output, "output", "", "output format: json")
		if action == "apply" {
			child.Flags().StringVar(&digest, "plan-digest", "", "digest returned by retention preview")
		}
		cmd.AddCommand(child)
	}
	return cmd
}
