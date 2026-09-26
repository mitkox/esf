package factory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mitkox/esf/internal/assurance"
)

type QualityProviderRequest struct {
	RunID           string    `json:"run_id"`
	ControlID       string    `json:"control_id"`
	Reference       string    `json:"reference"`
	CandidateDigest string    `json:"candidate_digest"`
	PlanDigest      string    `json:"plan_digest"`
	Deadline        time.Time `json:"deadline"`
	Ready           bool      `json:"ready"`
	Expired         bool      `json:"expired"`
}

// QualityProviderCheck records a durable acquisition request after all sandboxes
// are destroyed. A notification is only a wake-up hint; imported bytes and their
// transactional server timestamp determine whether the request was satisfied.
func (a *Activities) QualityProviderCheck(ctx context.Context, in QualityGateInput) (QualityProviderRequest, error) {
	r, _, _, err := a.qualityRecord(ctx, QualityRunInput{in.RunID, in.ExecutionID})
	if err != nil {
		return QualityProviderRequest{}, err
	}
	if r.Plan == nil || r.Candidate == nil || in.Control.Type != "provider-ack" {
		return QualityProviderRequest{}, assurance.ErrForbidden
	}
	found := false
	for _, g := range r.Plan.Controls {
		if assurance.Hash(g) == assurance.Hash(in.Control) {
			found = true
		}
	}
	if !found {
		return QualityProviderRequest{}, assurance.ErrForbidden
	}
	key := "provider-request/" + in.Control.ID
	request, exists, err := qualityCached[QualityProviderRequest](r, key)
	if err != nil {
		return request, err
	}
	if !exists {
		timeout, err := time.ParseDuration(in.Control.ProviderTimeout)
		if err != nil {
			return request, err
		}
		request = QualityProviderRequest{RunID: r.ID, ControlID: in.Control.ID, Reference: r.ID + "." + in.Control.Reference, CandidateDigest: r.Candidate.Digest, PlanDigest: r.Plan.Digest, Deadline: a.quality.Store.Now().Add(timeout)}
		if _, err = a.quality.Store.Checkpoint(ctx, r.ID, key, request); err != nil {
			return request, err
		}
	}
	if _, err = a.quality.Store.Object(ctx, "acknowledgment", request.Reference); err == nil {
		at, err := a.quality.Store.ImportedAt(ctx, "acknowledgment", request.Reference)
		if err != nil {
			return request, err
		}
		if at.Before(request.Deadline) {
			request.Ready = true
			return request, nil
		}
	} else if !errors.Is(err, assurance.ErrNotFound) {
		return request, err
	}
	request.Expired = !a.quality.Store.Now().Before(request.Deadline)
	return request, nil
}

func (a *Activities) qualityAcknowledgment(ctx context.Context, r assurance.Run, c assurance.Control) ([]byte, error) {
	request, ok, err := qualityCached[QualityProviderRequest](r, "provider-request/"+c.ID)
	if err != nil || !ok {
		return nil, fmt.Errorf("required provider acquisition request missing")
	}
	at, err := a.quality.Store.ImportedAt(ctx, "acknowledgment", request.Reference)
	if err != nil {
		return nil, fmt.Errorf("required provider acknowledgment unavailable: %w", err)
	}
	if !at.Before(request.Deadline) {
		return nil, fmt.Errorf("provider acknowledgment arrived after its deadline")
	}
	return a.quality.Store.Object(ctx, "acknowledgment", request.Reference)
}
