package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mitkox/esf/internal/forge"
)

const (
	defaultMaxSweepPages = 5
	maxSweepPagesLimit   = 100
	maxWatermarkBytes    = 4096
)

// SweeperOptions bounds one reconciliation sweep.
type SweeperOptions struct {
	// PageSize is requested from the forge per page. Zero uses the connector
	// default; values above the connector maximum are clamped.
	PageSize int
	// MaxPages bounds how many pages one sweep reads. A sweep that reaches the
	// bound returns the next token and the caller resumes there.
	MaxPages int
}

// Sweeper reads the reporting work-item revisions feed from a durable
// continuation-token watermark.
type Sweeper struct {
	client    *Client
	watermark forge.WatermarkStore
	pageSize  int
	maxPages  int
}

// NewSweeper constructs a bounded sweep.
func NewSweeper(client *Client, watermark forge.WatermarkStore, options SweeperOptions) (*Sweeper, error) {
	if client == nil {
		return nil, fmt.Errorf("azuredevops: sweep client is required")
	}
	if watermark == nil {
		return nil, fmt.Errorf("azuredevops: sweep watermark store is required")
	}
	pageSize := options.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	maxPages := options.MaxPages
	if maxPages <= 0 {
		maxPages = defaultMaxSweepPages
	}
	if maxPages > maxSweepPagesLimit {
		maxPages = maxSweepPagesLimit
	}
	return &Sweeper{client: client, watermark: watermark, pageSize: pageSize, maxPages: maxPages}, nil
}

// Sweep reads bounded pages from the watermark, keeps the newest revision per
// work item, re-fetches each work item to hash its authoritative bytes, and
// returns the candidates with the continuation token that follows them.
//
// The cursor is not saved: the caller delivers every candidate and only then
// calls CommitSweep. A throttled or failed read returns an error and leaves the
// stored watermark unchanged, so the next sweep resumes from the same token.
func (s *Sweeper) Sweep(ctx context.Context) (forge.SweepResult, error) {
	watermark, err := s.watermark.Load(ctx)
	if err != nil {
		return forge.SweepResult{}, fmt.Errorf("azuredevops: load sweep watermark: %w", err)
	}

	cursor := watermark
	latest := make(map[int64]int64)
	pages := 0
	for {
		page, err := s.client.FetchRevisions(ctx, cursor, s.pageSize)
		if err != nil {
			return forge.SweepResult{}, fmt.Errorf("azuredevops: sweep work item revisions: %w", err)
		}
		for _, revision := range page.Revisions {
			if previous, ok := latest[revision.WorkItemID]; !ok || revision.Revision > previous {
				latest[revision.WorkItemID] = revision.Revision
			}
		}
		pages++

		next := strings.TrimSpace(page.ContinuationToken)
		if next == "" {
			if !page.IsLastBatch {
				return forge.SweepResult{}, fmt.Errorf("azuredevops: revisions feed returned no continuation token before the last batch")
			}
			// Preserve the token that produced this batch: an empty token in
			// the response is not a request to replay from the beginning.
			next = cursor
		}
		if !page.IsLastBatch && next == cursor {
			return forge.SweepResult{}, fmt.Errorf("azuredevops: revisions feed continuation token did not advance")
		}
		if page.IsLastBatch || pages >= s.maxPages {
			cursor = next
			break
		}
		cursor = next
	}

	workItemIDs := make([]int64, 0, len(latest))
	for id := range latest {
		workItemIDs = append(workItemIDs, id)
	}
	sort.Slice(workItemIDs, func(i, j int) bool {
		if latest[workItemIDs[i]] != latest[workItemIDs[j]] {
			return latest[workItemIDs[i]] < latest[workItemIDs[j]]
		}
		return workItemIDs[i] < workItemIDs[j]
	})

	candidates := make([]forge.Candidate, 0, len(workItemIDs))
	for _, id := range workItemIDs {
		snapshot, err := s.client.FetchWorkItem(ctx, id)
		if err != nil {
			return forge.SweepResult{}, fmt.Errorf("azuredevops: sweep fetch work item %d: %w", id, err)
		}
		candidates = append(candidates, forge.Candidate{
			Connector:    s.client.Name(),
			Source:       forge.SourceSweep,
			WorkItem:     snapshot.WorkItem,
			WorkItemHash: snapshot.WorkItemHash,
			Actor:        snapshot.Actor,
		})
	}
	return forge.SweepResult{Items: candidates, NextCursor: cursor}, nil
}

// CommitSweep saves the continuation token that follows a fully delivered
// sweep. It is separate from Sweep so a failed delivery cannot advance the
// watermark.
func (s *Sweeper) CommitSweep(ctx context.Context, cursor string) error {
	if err := s.watermark.Save(ctx, cursor); err != nil {
		return fmt.Errorf("azuredevops: save sweep watermark: %w", err)
	}
	return nil
}

// FileWatermark stores a continuation-token watermark in one operator-owned
// file. Saves are atomic and fsynced before the rename is made durable.
type FileWatermark struct {
	path string
}

// NewFileWatermark validates the watermark file path.
func NewFileWatermark(path string) (*FileWatermark, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("azuredevops: watermark file must be an absolute path")
	}
	return &FileWatermark{path: path}, nil
}

// Load returns the stored watermark, or empty for a new store.
func (w *FileWatermark) Load(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(data) > maxWatermarkBytes {
		return "", fmt.Errorf("watermark exceeds %d bytes", maxWatermarkBytes)
	}
	watermark := strings.TrimSpace(string(data))
	if strings.ContainsAny(watermark, "\x00\r\n") {
		return "", fmt.Errorf("watermark contains control characters")
	}
	return watermark, nil
}

// Save atomically writes the watermark.
func (w *FileWatermark) Save(ctx context.Context, watermark string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	watermark = strings.TrimSpace(watermark)
	if len(watermark) > maxWatermarkBytes {
		return fmt.Errorf("watermark exceeds %d bytes", maxWatermarkBytes)
	}
	if strings.ContainsAny(watermark, "\x00\r\n") {
		return fmt.Errorf("watermark contains control characters")
	}
	dir := filepath.Dir(w.path)
	tmp, err := os.CreateTemp(dir, ".watermark-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(watermark); err != nil {
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
	if err := os.Rename(tmpName, w.path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
