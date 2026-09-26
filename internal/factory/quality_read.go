package factory

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/mitkox/esf/internal/assurance"
)

// ReadRunManifest consults the authority before reading compatibility exports.
// Service outages never silently fall back to a potentially stale quality file.
func (r *Runtime) ReadRunManifest(ctx context.Context, id string) (RunManifest, error) {
	if r.Config.Quality.Enabled() {
		var record assurance.Run
		err := NewQualityClient(r.Config.QualitySocket()).Call(ctx, "GET", "/v1/runs/"+id, nil, &record)
		if err == nil {
			if record.Decision == nil {
				return RunManifest{}, errors.New("controlled run has no terminal decision")
			}
			var m RunManifest
			err = json.Unmarshal(record.Manifest, &m)
			return m, err
		}
		if !errors.Is(err, assurance.ErrNotFound) {
			return RunManifest{}, err
		}
	}
	return ReadManifest(r.Artifacts, id)
}
func (r *Runtime) RunIDs(ctx context.Context) ([]string, error) {
	ids, err := ListRunIDs(r.Artifacts)
	if err != nil {
		return nil, err
	}
	if !r.Config.Quality.Enabled() {
		return ids, nil
	}
	var records []assurance.Run
	if err = NewQualityClient(r.Config.QualitySocket()).Call(ctx, "GET", "/v1/runs", nil, &records); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, r := range records {
		if !seen[r.ID] {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

// qualityChanges is a view over admission and terminal records. A prior DONE
// activation never carries authorization to a later pending or failed run.
func (r *Runtime) qualityChanges(ctx context.Context) ([]Change, error) {
	runs, err := r.Quality.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
	byID := map[string]Change{}
	legacy, err := NewChangeStore(r.Artifacts.Root())
	if err != nil {
		return nil, err
	}
	old, err := legacy.List()
	if err != nil {
		return nil, err
	}
	for _, c := range old {
		byID[c.ChangeID] = c
	}
	for _, record := range runs {
		var req RunRequest
		if err = json.Unmarshal(record.Request, &req); err != nil {
			return nil, err
		}
		id := req.ChangeID
		if id == "" {
			id = record.ID
		}
		c, ok := byID[id]
		if !ok {
			repo := req.Repository
			if repo == "" {
				repo = "local:" + req.LocalPath
			}
			c = Change{ChangeID: id, Scope: req.Scope, Repository: repo, RequestedRevision: req.Revision, Task: req.Task, CreatedAt: record.CreatedAt}
		}
		exists := false
		for _, a := range c.Activations {
			if a.RunID == record.ID {
				exists = true
			}
		}
		if !exists {
			reason := ActivationInitial
			if req.ParentRunID != "" {
				reason = ActivationRework
			}
			c.Activations = append(c.Activations, Activation{RunID: record.ID, ParentRunID: req.ParentRunID, Reason: reason, Task: req.Task, Scope: req.Scope, CreatedAt: record.CreatedAt})
		}
		c.Attempts = len(c.Activations)
		c.LastRunID = record.ID
		c.Status = ChangeOpen
		c.LastResult = StateRunning
		c.UpdatedAt = record.CreatedAt
		if record.Approval != nil && record.Approval.Status == "pending" {
			c.Status = ChangeInReview
		}
		if record.Decision != nil {
			var m RunManifest
			if err = json.Unmarshal(record.Manifest, &m); err != nil {
				return nil, err
			}
			c.LastResult = m.FactoryResult
			c.UpdatedAt = record.Decision.At
			if record.Decision.Result == "approved" && m.FactoryResult == StateSucceeded {
				c.Status = ChangeDone
			}
			if !exists {
				if m.InferenceCost != nil {
					c.TotalCostUSD += *m.InferenceCost
				}
				if m.TokensIn != nil {
					c.TotalTokens += *m.TokensIn
				}
				if m.TokensOut != nil {
					c.TotalTokens += *m.TokensOut
				}
			}
		}
		byID[id] = c
	}
	out := make([]Change, 0, len(byID))
	for _, c := range byID {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ChangeID < out[j].ChangeID })
	return out, nil
}
