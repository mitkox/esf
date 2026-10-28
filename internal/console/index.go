package console

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mitkox/esf/internal/factory"
)

// evidenceIndex is an in-memory index over the durable artifact store.
//
// It is deliberately incremental: a refresh stats each manifest and only
// re-reads files whose size or modification time changed. The index holds the
// parsed manifests, so list, overview and security queries never touch the
// filesystem on the request path. That is what keeps 10k-run deployments fast
// while remaining a faithful projection of evidence.
type evidenceIndex struct {
	root        string
	maxAge      time.Duration
	threatLimit int
	now         func() time.Time

	refreshMu sync.Mutex

	mu            sync.RWMutex
	runs          map[string]*runRecord
	changes       map[string]*changeRecord
	runOrder      []*runRecord
	changeOrder   []*changeRecord
	refreshedAt   time.Time
	scanMS        int64
	scanErrors    int
	lastScanError string
	oldestRunAt   time.Time
	newestRunAt   time.Time
	threat        factory.ThreatReport
}

type runRecord struct {
	ID       string
	Manifest factory.RunManifest
	ModTime  time.Time
	Size     int64
}

type changeRecord struct {
	ID      string
	Change  factory.Change
	ModTime time.Time
	Size    int64
}

func newEvidenceIndex(root string, maxAge time.Duration, threatLimit int, now func() time.Time) *evidenceIndex {
	if maxAge <= 0 {
		maxAge = 15 * time.Second
	}
	if threatLimit <= 0 {
		threatLimit = 100
	}
	if now == nil {
		now = time.Now
	}
	return &evidenceIndex{
		root:        root,
		maxAge:      maxAge,
		threatLimit: threatLimit,
		now:         now,
		runs:        map[string]*runRecord{},
		changes:     map[string]*changeRecord{},
	}
}

// refresh updates the index when it is older than maxAge, or unconditionally
// when force is set. Concurrent callers are serialised; a caller that loses the
// race observes the freshly built index instead of rebuilding it.
func (i *evidenceIndex) refresh(force bool) error {
	if !force {
		i.mu.RLock()
		fresh := !i.refreshedAt.IsZero() && i.now().Sub(i.refreshedAt) < i.maxAge
		i.mu.RUnlock()
		if fresh {
			return nil
		}
	}

	i.refreshMu.Lock()
	defer i.refreshMu.Unlock()

	i.mu.RLock()
	if !force && !i.refreshedAt.IsZero() && i.now().Sub(i.refreshedAt) < i.maxAge {
		i.mu.RUnlock()
		return nil
	}
	previousRuns := i.runs
	previousChanges := i.changes
	i.mu.RUnlock()

	started := i.now()
	newRuns := map[string]*runRecord{}
	scanErrors := 0

	runsDir := filepath.Join(i.root, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil && !os.IsNotExist(err) {
		i.recordScanFailure(fmt.Errorf("read evidence runs directory: %w", err))
		return fmt.Errorf("read evidence runs directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if !validEvidenceID(id) {
			continue
		}
		info, err := os.Stat(filepath.Join(runsDir, id, "run.json"))
		if err != nil {
			if !os.IsNotExist(err) {
				scanErrors++
			}
			continue
		}
		if previous := previousRuns[id]; previous != nil && previous.ModTime.Equal(info.ModTime()) && previous.Size == info.Size() {
			newRuns[id] = previous
			continue
		}
		data, err := os.ReadFile(filepath.Join(runsDir, id, "run.json"))
		if err != nil {
			scanErrors++
			if previous := previousRuns[id]; previous != nil {
				newRuns[id] = previous
			}
			continue
		}
		var manifest factory.RunManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			scanErrors++
			if previous := previousRuns[id]; previous != nil {
				newRuns[id] = previous
			}
			continue
		}
		newRuns[id] = &runRecord{ID: id, Manifest: manifest, ModTime: info.ModTime(), Size: info.Size()}
	}

	newChanges := map[string]*changeRecord{}
	changesDir := filepath.Join(i.root, factory.ChangeDirName)
	changeEntries, err := os.ReadDir(changesDir)
	if err != nil && !os.IsNotExist(err) {
		i.recordScanFailure(fmt.Errorf("read evidence changes directory: %w", err))
		return fmt.Errorf("read evidence changes directory: %w", err)
	}
	for _, entry := range changeEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !validEvidenceID(id) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			scanErrors++
			continue
		}
		if previous := previousChanges[id]; previous != nil && previous.ModTime.Equal(info.ModTime()) && previous.Size == info.Size() {
			newChanges[id] = previous
			continue
		}
		data, err := os.ReadFile(filepath.Join(changesDir, entry.Name()))
		if err != nil {
			scanErrors++
			if previous := previousChanges[id]; previous != nil {
				newChanges[id] = previous
			}
			continue
		}
		var change factory.Change
		if err := json.Unmarshal(data, &change); err != nil {
			scanErrors++
			if previous := previousChanges[id]; previous != nil {
				newChanges[id] = previous
			}
			continue
		}
		if change.ChangeID == "" {
			change.ChangeID = id
		}
		newChanges[id] = &changeRecord{ID: id, Change: change, ModTime: info.ModTime(), Size: info.Size()}
	}

	runOrder := make([]*runRecord, 0, len(newRuns))
	for _, record := range newRuns {
		runOrder = append(runOrder, record)
	}
	sort.Slice(runOrder, func(a, b int) bool {
		left, right := runSortTime(runOrder[a]), runSortTime(runOrder[b])
		if left.Equal(right) {
			return runOrder[a].ID > runOrder[b].ID
		}
		return left.After(right)
	})

	changeOrder := make([]*changeRecord, 0, len(newChanges))
	for _, record := range newChanges {
		changeOrder = append(changeOrder, record)
	}
	sort.Slice(changeOrder, func(a, b int) bool {
		left, right := changeOrder[a].Change.UpdatedAt, changeOrder[b].Change.UpdatedAt
		if left.Equal(right) {
			return changeOrder[a].ID > changeOrder[b].ID
		}
		return left.After(right)
	})

	var oldest, newest time.Time
	identified := make([]factory.IdentifiedManifest, 0, len(runOrder))
	for _, record := range runOrder {
		identified = append(identified, factory.IdentifiedManifest{RunID: record.ID, Manifest: record.Manifest})
		startedAt := record.Manifest.StartedAt
		if startedAt.IsZero() {
			startedAt = record.Manifest.CompletedAt
		}
		if startedAt.IsZero() {
			startedAt = record.ModTime
		}
		if oldest.IsZero() || startedAt.Before(oldest) {
			oldest = startedAt
		}
		if newest.IsZero() || startedAt.After(newest) {
			newest = startedAt
		}
	}
	threat := factory.AggregateThreatReport(identified, i.threatLimit)

	finished := i.now()
	i.mu.Lock()
	i.runs = newRuns
	i.changes = newChanges
	i.runOrder = runOrder
	i.changeOrder = changeOrder
	i.refreshedAt = finished
	i.scanMS = finished.Sub(started).Milliseconds()
	i.scanErrors = scanErrors
	if scanErrors > 0 {
		i.lastScanError = fmt.Sprintf("%d evidence entr%s could not be read; showing the last complete index", scanErrors, plural(scanErrors))
	} else {
		i.lastScanError = ""
	}
	i.oldestRunAt = oldest
	i.newestRunAt = newest
	i.threat = threat
	i.mu.Unlock()
	return nil
}

func validEvidenceID(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 200 {
		return false
	}
	return !strings.ContainsAny(id, `/\`+"\x00")
}

func runSortTime(record *runRecord) time.Time {
	if !record.Manifest.StartedAt.IsZero() {
		return record.Manifest.StartedAt
	}
	if !record.Manifest.CompletedAt.IsZero() {
		return record.Manifest.CompletedAt
	}
	return record.ModTime
}

// snapshot returns the current ordered records and index metadata. Records are
// immutable after publication, so callers may inspect them without holding the
// index lock.
func (i *evidenceIndex) snapshot() ([]*runRecord, []*changeRecord, IndexInfo) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	info := IndexInfo{
		Runs:        len(i.runs),
		Changes:     len(i.changes),
		RefreshedAt: timePtr(i.refreshedAt),
		ScanMS:      i.scanMS,
		ScanErrors:  i.scanErrors,
		ScanError:   i.lastScanError,
		Stale:       i.lastScanError != "",
		OldestRunAt: timePtr(i.oldestRunAt),
		NewestRunAt: timePtr(i.newestRunAt),
	}
	return i.runOrder, i.changeOrder, info
}

func (i *evidenceIndex) threatReport() factory.ThreatReport {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.threat
}

func (i *evidenceIndex) run(id string) (*runRecord, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	record, ok := i.runs[id]
	return record, ok
}

func (i *evidenceIndex) change(id string) (*changeRecord, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	record, ok := i.changes[id]
	return record, ok
}

func (i *evidenceIndex) runsForChange(changeID string) []*runRecord {
	if changeID == "" {
		return nil
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	var records []*runRecord
	for _, record := range i.runs {
		if record.Manifest.ChangeID == changeID {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(a, b int) bool { return runSortTime(records[a]).After(runSortTime(records[b])) })
	return records
}

func (i *evidenceIndex) recordScanFailure(err error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.lastScanError = err.Error()
}

func plural(count int) string {
	if count == 1 {
		return "y"
	}
	return "ies"
}
