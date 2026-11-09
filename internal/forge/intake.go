package forge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Source identifies the intake path that produced a work-item candidate.
type Source string

const (
	// SourceSweep marks a candidate recovered by reconciliation.
	SourceSweep Source = "sweep"
	// SourceWebhook marks a candidate accelerated by a delivered event.
	SourceWebhook Source = "webhook"
)

// Actor is a forge identity resolved through the forge API.
//
// The descriptor is the API-resolved identity used for permission checks. A
// webhook payload's display name is never copied here.
type Actor struct {
	ID          string
	DisplayName string
	UniqueName  string
	Descriptor  string
}

// Repository permissions reported by a forge.
const (
	// PermissionWrite means the actor may contribute to the mapped repository.
	PermissionWrite = "write"
)

// Candidate is a reconciliation result: a re-fetched work item and the digest
// of the exact bytes the forge returned.
type Candidate struct {
	Connector    string
	Source       Source
	WorkItem     WorkItem
	WorkItemHash string
	// Actor is the forge-resolved identity from the authoritative work-item
	// response. A sweep must not copy a display name from an event payload.
	Actor Actor
}

// SweepResult is one bounded reconciliation pass.
//
// NextCursor is the continuation token that follows Items. It is not durable
// until the caller delivered every item and recorded it: a failed or cancelled
// delivery leaves the previous cursor in place, so the next sweep returns the
// same items and the workflow identity makes the replay idempotent.
type SweepResult struct {
	Items      []Candidate
	NextCursor string
}

// Proposal is an authorized intake event that may start or signal a run.
//
// It carries the operator-mapped work item, the API-resolved actor, and the
// delivery identity used for deduplication. It never carries a clone or forge
// credential. The actor comes from the embedded Candidate and is deliberately
// declared only there: a second, shallower Actor field would shadow the
// candidate's resolved identity during JSON encoding and silently drop it.
type Proposal struct {
	Candidate
	DeliveryID    string
	ChangedFields []string
	// RepositoryPermission is the permission the forge API reported for Actor
	// on the mapped repository. It is evidence that authorization was resolved
	// from the forge, never from a payload display string.
	RepositoryPermission string
}

// Rejection is an intake event that was not proposed as a run.
//
// The sink records it for audit; no run is started.
type Rejection struct {
	Connector  string
	WorkItemID string
	Revision   string
	Actor      Actor
	Reason     string
}

// IntakeSink receives the outcome of intake processing.
//
// Propose is called only after the work item has been re-fetched, mapped to an
// operator-selected repository, and authorized against that repository. Reject
// records the audited no-run decision.
type IntakeSink interface {
	Propose(ctx context.Context, proposal Proposal) error
	Reject(ctx context.Context, rejection Rejection) error
}

// RepositoryResolver maps bounded work-item attributes to an operator-selected
// repository URL. It is the only source of a repository during intake.
type RepositoryResolver interface {
	ResolveRepository(fields WorkItemFields) (string, error)
}

// RepositoryResolverFunc adapts a function to RepositoryResolver.
type RepositoryResolverFunc func(fields WorkItemFields) (string, error)

// ResolveRepository calls f.
func (f RepositoryResolverFunc) ResolveRepository(fields WorkItemFields) (string, error) {
	return f(fields)
}

// WatermarkStore durably stores a forge continuation token.
//
// A sweep records a token only after every work item it returned has been
// delivered, so a failed delivery resumes from the same watermark.
type WatermarkStore interface {
	Load(ctx context.Context) (string, error)
	Save(ctx context.Context, watermark string) error
}

// DigestWorkItem hashes the exact bytes a forge returned for one work item.
//
// The digest is over the raw response, before any decoding or normalization,
// so it identifies the bytes shown to the pipeline.
func DigestWorkItem(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
