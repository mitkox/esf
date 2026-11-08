package forge

import "context"

// Fake is an in-memory Connector for tests and for later adapters' contract
// tests. It records calls and returns configurable results and errors.
type Fake struct {
	Caps Capabilities

	ReconcileResult  ReconcileResult
	PublishResult    PublishResult
	AcceptanceResult AcceptanceResult
	// AcceptanceResults, when non-empty, overrides AcceptanceResult per call,
	// so a test can model an open pull request that merges later. The last
	// entry repeats after the slice is exhausted.
	AcceptanceResults []AcceptanceResult
	BranchResult      BranchPublicationResult
	// WorkItemState is the state ReadWorkItem reports. Empty leaves the state
	// unset, which publication treats as not withdrawn.
	WorkItemState string

	ReportErr       error
	ReconcileErr    error
	PublishErr      error
	AcceptanceErr   error
	BranchErr       error
	WorkItemReadErr error

	Reports         []ReportRequest
	Reconciliations []ReconcileRequest
	Publications    []PublicationPlan
	Acceptances     []AcceptanceRequest
	Branches        []BranchPublication
	Builds          []BranchPublication

	acceptanceCalls int
}

var _ Connector = (*Fake)(nil)
var _ BranchPublisher = (*Fake)(nil)
var _ WorkItemStateReader = (*Fake)(nil)

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
	if len(f.AcceptanceResults) > 0 {
		index := f.acceptanceCalls
		if index >= len(f.AcceptanceResults) {
			index = len(f.AcceptanceResults) - 1
		}
		f.acceptanceCalls++
		return f.AcceptanceResults[index], f.AcceptanceErr
	}
	f.acceptanceCalls++
	return f.AcceptanceResult, f.AcceptanceErr
}

// ReadWorkItem reports the configured current work-item state.
func (f *Fake) ReadWorkItem(_ context.Context, id string) (WorkItem, error) {
	if f.WorkItemReadErr != nil {
		return WorkItem{}, f.WorkItemReadErr
	}
	return WorkItem{ID: id, WorkItemFields: WorkItemFields{State: f.WorkItemState}}, nil
}

// BuildBranch records a host-side build and returns the configured tree
// without touching the remote.
func (f *Fake) BuildBranch(_ context.Context, req BranchPublication) (BranchBuild, error) {
	f.Builds = append(f.Builds, req)
	if f.BranchErr != nil {
		return BranchBuild{}, f.BranchErr
	}
	return BranchBuild{
		Branch:             req.Branch,
		Commit:             f.BranchResult.Commit,
		Tree:               f.BranchResult.Tree,
		ChangedPaths:       append([]string(nil), f.BranchResult.ChangedPaths...),
		GitmodulesBaseline: append([]byte(nil), f.BranchResult.GitmodulesBaseline...),
		GitmodulesResult:   append([]byte(nil), f.BranchResult.GitmodulesResult...),
	}, nil
}

// PushBranch records the pushed branch and returns the configured result.
func (f *Fake) PushBranch(_ context.Context, req BranchPublication, _ BranchBuild) (BranchPublicationResult, error) {
	f.Branches = append(f.Branches, req)
	return f.BranchResult, f.BranchErr
}

// DiscardBranch releases nothing; the fake holds no checkout.
func (f *Fake) DiscardBranch(BranchBuild) {}
