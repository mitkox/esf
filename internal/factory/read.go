package factory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mitkox/esf/internal/artifacts"
)

// ReadManifest loads a run's durable manifest from the artifact store.
//
// This is the authoritative record of a finished run: it survives sandbox
// destruction, process restarts and Temporal history retention.
func ReadManifest(factory artifacts.Factory, runID string) (RunManifest, error) {
	store, err := factory.ForRun(runID)
	if err != nil {
		return RunManifest{}, err
	}
	data, err := store.Read(ArtifactRun)
	if err != nil {
		return RunManifest{}, fmt.Errorf("read manifest for run %s: %w", runID, err)
	}
	var manifest RunManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return RunManifest{}, fmt.Errorf("decode manifest for run %s: %w", runID, err)
	}
	return manifest, nil
}

// ReadArtifact loads one artifact from a run.
func ReadArtifact(factory artifacts.Factory, runID, relPath string) ([]byte, error) {
	store, err := factory.ForRun(runID)
	if err != nil {
		return nil, err
	}
	return store.Read(relPath)
}

// ListRunIDs returns the run IDs present in the artifact store, newest first.
//
// The list is derived from the filesystem because the artifact store, not
// Temporal, is the durable record: a run whose workflow history has expired is
// still a run an operator must be able to find.
func ListRunIDs(factory artifacts.Factory) ([]string, error) {
	runsDir := filepath.Join(factory.Root(), "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list runs: %w", err)
	}
	type entry struct {
		id      string
		modTime time.Time
	}
	var found []entry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		found = append(found, entry{id: e.Name(), modTime: info.ModTime()})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].modTime.After(found[j].modTime) })
	ids := make([]string, 0, len(found))
	for _, e := range found {
		ids = append(ids, e.id)
	}
	return ids, nil
}
