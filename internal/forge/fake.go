package forge

import "context"

// Fake is an in-memory Connector for tests and for later adapters' contract
// tests. It records calls and returns configurable results and errors.
type Fake struct {
	Caps Capabilities

	ReconcileResult  ReconcileResult
	PublishResult    PublishResult
	AcceptanceResult AcceptanceResult
	BranchResult     BranchPublicationResult

	ReportErr     error
	ReconcileErr  error
	PublishErr    error
	AcceptanceErr error
	BranchErr     error

	Reports         []ReportRequest
	Reconciliations []ReconcileRequest
	Publications    []PublicationPlan
	Acceptances     []AcceptanceRequest
	Branches        []BranchPublication
}

var _ Connector = (*Fake)(nil)
var _ BranchPublisher = (*Fake)(nil)

// NewFake creates a fake connector with the given capabilities.
func NewFake(caps Capabilities) *Fake { return &Fake{Caps: caps} }

// Capabilities returns the configured capability set.
func (f *Fake) Capabilities() Capabilities { return f.Caps }

// Report records a report request and returns the configured error.
func (f *Fake) Report(_ context.Context, req ReportRequest) error {
	f.Reports = append(f.Reports, req)
	return f.ReportErr
}

// Reconcile records a reconciliation request and returns the configured page.
func (f *Fake) Reconcile(_ context.Context, req ReconcileRequest) (ReconcileResult, error) {
	f.Reconciliations = append(f.Reconciliations, req)
	return f.ReconcileResult, f.ReconcileErr
}

// Publish records a publication plan and returns the configured pull request.
func (f *Fake) Publish(_ context.Context, plan PublicationPlan) (PublishResult, error) {
	f.Publications = append(f.Publications, plan)
	return f.PublishResult, f.PublishErr
}

// ReadAcceptance records an acceptance request and returns the configured
// acceptance state.
func (f *Fake) ReadAcceptance(_ context.Context, req AcceptanceRequest) (AcceptanceResult, error) {
	f.Acceptances = append(f.Acceptances, req)
	return f.AcceptanceResult, f.AcceptanceErr
}

// PublishBranch records a branch publication and returns the configured result.
func (f *Fake) PublishBranch(_ context.Context, req BranchPublication) (BranchPublicationResult, error) {
	f.Branches = append(f.Branches, req)
	return f.BranchResult, f.BranchErr
}
