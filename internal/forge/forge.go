// Package forge defines the host-side contract between the factory and a forge
// platform.
//
// A connector is an adapter below the factory in the dependency graph. It reads
// and writes forge work items and pull requests, and it holds a forge
// credential that must never enter a Cube sandbox. The contract deliberately
// separates planning from publication: the factory composes a pull-request body
// from the signed evidence bundle, and the adapter only performs the forge
// calls.
//
// Operator configuration alone decides which repository, scope, base branch,
// and verification profile a work item maps to. The mapping input contains only
// the forge attributes a rule may match; it has no repository, revision,
// executable, credential, or host-path field.
package forge

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mitkox/esf/internal/secretfile"
)

// maxCredentialBytes bounds a forge credential read from a private file.
const maxCredentialBytes = 4096

// EvidenceBundleAttachmentName is the file name used when a forge can attach
// the full evidence bundle to a pull request.
const EvidenceBundleAttachmentName = "evidence-bundle.json"

// VerificationStatusName is the forge status name attached to a published
// change. It is deliberately stable across releases so a branch policy can
// require it.
const VerificationStatusName = "esf/verification"

// Credential is a forge credential read from an operator-owned private file.
//
// Its value is deliberately not printable: formatting a Credential cannot leak
// the secret into a log or an error message.
type Credential struct {
	value string
}

// ReadCredential reads a forge credential through the private-file rules: a
// regular file, no symlink, owner-only permissions, and a bounded size.
func ReadCredential(path string) (Credential, error) {
	data, err := secretfile.Read(path, maxCredentialBytes)
	if err != nil {
		return Credential{}, err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return Credential{}, fmt.Errorf("credential is empty")
	}
	for _, r := range value {
		if r <= ' ' || r == 0x7f {
			return Credential{}, fmt.Errorf("credential contains whitespace or control characters")
		}
	}
	return Credential{value: value}, nil
}

// Value returns the credential for a host-side forge client.
//
// The caller must pass it directly to the client and must never log, persist,
// or place it in workflow input.
func (c Credential) Value() string { return c.value }

// String redacts the credential.
func (c Credential) String() string { return "[redacted]" }

// GoString redacts the credential in %#v formatting.
func (c Credential) GoString() string { return "forge.Credential([redacted])" }

// Kind identifies a forge platform.
type Kind string

const (
	// KindAzureDevOps identifies Azure DevOps.
	KindAzureDevOps Kind = "azuredevops"
	// KindGitHub identifies GitHub.
	KindGitHub Kind = "github"
	// KindGitLab identifies GitLab.
	KindGitLab Kind = "gitlab"
)

// Valid reports whether the forge kind is supported by the connector contract.
func (k Kind) Valid() bool {
	switch k {
	case KindAzureDevOps, KindGitHub, KindGitLab:
		return true
	default:
		return false
	}
}

// ConnectorSettings is the resolved operator configuration for one connector.
//
// It is construction input for a host-side adapter. The credential is read only
// when an adapter is constructed, and it is never recorded in configuration,
// workflow history, or evidence.
type ConnectorSettings struct {
	Name       string
	Kind       Kind
	BaseURL    string
	Credential Credential
}

// Connector is the contract every forge adapter implements.
type Connector interface {
	// Capabilities reports the forge features that bound publication.
	Capabilities() Capabilities
	// Report posts an outcome or status to the forge.
	Report(ctx context.Context, req ReportRequest) error
	// Reconcile reads work-item revisions after a cursor.
	Reconcile(ctx context.Context, req ReconcileRequest) (ReconcileResult, error)
	// Publish creates the branch, pull request, and attachment represented by
	// a plan. It never merges or approves a pull request; status reporting is
	// the separate Report operation.
	Publish(ctx context.Context, plan PublicationPlan) (PublishResult, error)
	// ReadAcceptance reads the forge-native merge state of a pull request.
	ReadAcceptance(ctx context.Context, req AcceptanceRequest) (AcceptanceResult, error)
}

// WorkItemFields are the only work-item attributes an operator mapping rule may
// match. A delivered payload cannot select a repository, revision, executable,
// credential, or host path because none of them is representable here.
type WorkItemFields struct {
	Project string
	Area    string
	Type    string
	State   string
	Labels  []string
}

// WorkItem is a re-fetched forge work item.
//
// Revision is the forge's work-item revision used for deduplication; it is not
// a git revision. The body is untrusted input and is never a publication
// target.
type WorkItem struct {
	ID       string
	Revision string
	WorkItemFields
	Title string
	Body  string
}

// MappingFields returns the bounded attributes available to operator mapping.
func (w WorkItem) MappingFields() WorkItemFields { return w.WorkItemFields }

// WorkItemRef identifies one work-item revision without carrying its content.
type WorkItemRef struct {
	ID       string
	Revision string
	Project  string
}

// StatusState is the outcome reported to a forge.
type StatusState string

const (
	// StatusPending reports work still in progress.
	StatusPending StatusState = "pending"
	// StatusSucceeded reports a successful outcome.
	StatusSucceeded StatusState = "succeeded"
	// StatusFailed reports a failed outcome.
	StatusFailed StatusState = "failed"
	// StatusError reports an error that prevents an outcome.
	StatusError StatusState = "error"
)

// Status is one forge-native status update.
type Status struct {
	// Name is stable across releases so a branch policy can require it.
	Name        string
	State       StatusState
	Description string
	TargetURL   string
}

// ReportRequest reports an outcome on a work item, a pull request, or both.
type ReportRequest struct {
	WorkItem WorkItemRef
	// Repository is the operator-mapped repository that owns PullRequestID. It
	// is required only when a status is posted to a pull request.
	Repository    string
	PullRequestID string
	Status        Status
	Comment       string
}

// ReconcileRequest asks for work-item revisions after a continuation cursor.
type ReconcileRequest struct {
	// Cursor is the adapter's continuation token. Empty starts from the
	// operator-selected beginning.
	Cursor string
	// Limit bounds the number of work items returned. Zero uses the adapter's
	// configured page size.
	Limit int
}

// ReconcileResult is one page of reconciliation candidates.
type ReconcileResult struct {
	WorkItems []WorkItem
	// Cursor is the next continuation token. Empty means the page is complete.
	Cursor string
}

// Attachment is a file published with a pull request.
type Attachment struct {
	Name      string
	MediaType string
	Data      []byte
}

// Publication is the factory-owned material for a pull request. Summary and
// BundleDigest come from the signed evidence bundle, never from agent output.
type Publication struct {
	WorkItem     WorkItemRef
	Repository   string
	BaseBranch   string
	Branch       string
	Title        string
	Summary      string
	BundleDigest string
	Bundle       []byte
	Draft        bool
}

// PublicationPlan is a publication bounded by the connector's capabilities.
type PublicationPlan struct {
	WorkItem     WorkItemRef
	Repository   string
	BaseBranch   string
	Branch       string
	Title        string
	Body         string
	Draft        bool
	Attachment   *Attachment
	StatusKind   StatusKind
	BundleDigest string
}

// PullRequest identifies a published pull request.
type PullRequest struct {
	ID    string
	URL   string
	Draft bool
	// Commit and Tree identify the source revision when the forge reports it.
	Commit string
	Tree   string
}

// PublishResult is the forge's response to a publication.
type PublishResult struct {
	PullRequest PullRequest
}

// BranchPublication is a host-built branch the forge should create.
//
// The patch is applied by the factory host, never inside a sandbox. The commit
// is deterministic so a retried publication addresses the same branch state.
type BranchPublication struct {
	Repository string
	// RemoteURL overrides the git remote used for the push. It is empty in
	// production, where Repository is already the push URL.
	RemoteURL   string
	BaselineSHA string
	Branch      string
	ChangeID    string
	Attempt     int
	Message     string
	AuthorName  string
	AuthorEmail string
	CommitTime  time.Time
	Patch       []byte
}

// BranchPublicationResult describes the branch that now exists on the forge.
type BranchPublicationResult struct {
	Branch             string
	Commit             string
	Tree               string
	ChangedPaths       []string
	GitmodulesBaseline []byte
	GitmodulesResult   []byte
	AlreadyPresent     bool
}

// BranchBuild is a host-built publication commit that has not been pushed.
//
// It exists so every publication gate can run against the exact built tree
// before the commit reaches the forge. Handle is adapter-owned cleanup state;
// the factory never inspects or persists it.
type BranchBuild struct {
	Branch             string
	Commit             string
	Tree               string
	ChangedPaths       []string
	GitmodulesBaseline []byte
	GitmodulesResult   []byte
	Handle             any
}

// BranchPublisher is an optional connector capability: a forge adapter that
// can build the branch a pull request needs and push it separately.
//
// The factory builds first, evaluates every publication gate against the built
// tree, and only then calls PushBranch. A connector that cannot split the two
// steps cannot enforce a pre-push gate and is not accepted as a publisher.
type BranchPublisher interface {
	BuildBranch(ctx context.Context, req BranchPublication) (BranchBuild, error)
	PushBranch(ctx context.Context, req BranchPublication, build BranchBuild) (BranchPublicationResult, error)
	DiscardBranch(build BranchBuild)
}

// WorkItemStateReader is an optional connector capability: read the
// authoritative current state of a work item by id.
//
// Publication re-reads the work item through this interface immediately before
// a remote write, so a work item withdrawn after intake opens no branch or pull
// request.
type WorkItemStateReader interface {
	ReadWorkItem(ctx context.Context, id string) (WorkItem, error)
}

// ApprovalVote is one reviewer vote read from the forge.
type ApprovalVote struct {
	Reviewer    string
	DisplayName string
	Descriptor  string
	Vote        int
}

// AcceptanceRequest asks for the forge-native state of a pull request.
type AcceptanceRequest struct {
	Repository    string
	PullRequestID string
}

// AcceptanceState is the forge's merge state.
type AcceptanceState string

const (
	// AcceptanceOpen means the pull request is still open.
	AcceptanceOpen AcceptanceState = "open"
	// AcceptanceMerged means the developer merged the pull request.
	AcceptanceMerged AcceptanceState = "merged"
	// AcceptanceClosed means the pull request was closed without merging.
	AcceptanceClosed AcceptanceState = "closed"
)

// Valid reports whether the acceptance state is understood by the factory.
func (s AcceptanceState) Valid() bool {
	switch s {
	case AcceptanceOpen, AcceptanceMerged, AcceptanceClosed:
		return true
	default:
		return false
	}
}

// AcceptanceResult is the acceptance state read from the forge.
//
// MergeTree is the tree of the merge commit. The factory accepts a merged pull
// request only when it matches the tree derived from the evidenced patch.
type AcceptanceResult struct {
	PullRequest       PullRequest
	State             AcceptanceState
	MergeCommit       string
	MergeTree         string
	SourceCommit      string
	Votes             []ApprovalVote
	AutoCompleteSetBy string
	ClosedBy          string
	ClosedAt          time.Time
}

// digestLabel prefixes the mandatory evidence digest in a pull-request body.
const digestLabel = "Evidence bundle digest: "

// attachmentNotice is appended only when the connector will attach the bundle.
const attachmentNotice = "\nFull evidence bundle attached."

// PlanPublication composes a pull-request body that fits the forge's maximum
// body size and decides whether the full bundle travels as an attachment.
//
// The evidence digest is mandatory and is never truncated. The summary may be
// shortened when a forge has a small body cap; when even the digest cannot fit,
// planning fails closed instead of publishing an unsupported body.
func PlanPublication(caps Capabilities, pub Publication) (PublicationPlan, error) {
	if err := caps.Validate(); err != nil {
		return PublicationPlan{}, err
	}
	if pub.Draft && !caps.DraftPullRequests {
		return PublicationPlan{}, fmt.Errorf("forge does not support draft pull requests")
	}
	title := strings.TrimSpace(pub.Title)
	if title == "" {
		return PublicationPlan{}, fmt.Errorf("publication title is required")
	}
	if strings.ContainsAny(title, "\x00\r\n") {
		return PublicationPlan{}, fmt.Errorf("publication title contains control characters")
	}
	digest := strings.TrimSpace(pub.BundleDigest)
	if digest == "" {
		return PublicationPlan{}, fmt.Errorf("evidence bundle digest is required")
	}
	if strings.ContainsAny(digest, " \t\r\n\x00") {
		return PublicationPlan{}, fmt.Errorf("evidence bundle digest contains whitespace or control characters")
	}

	var attachment *Attachment
	suffix := digestLabel + digest
	if caps.Attachments && len(pub.Bundle) > 0 {
		attachment = &Attachment{
			Name:      EvidenceBundleAttachmentName,
			MediaType: "application/json",
			Data:      append([]byte(nil), pub.Bundle...),
		}
		suffix += attachmentNotice
	}
	if len(suffix) > caps.MaxBodyBytes {
		return PublicationPlan{}, fmt.Errorf("forge body limit of %d bytes is too small for the evidence digest", caps.MaxBodyBytes)
	}

	summary := normalizeSummary(pub.Summary)
	body := suffix
	if summary != "" {
		room := caps.MaxBodyBytes - len(suffix) - len("\n\n")
		if shortened := truncateUTF8(summary, room); shortened != "" {
			body = shortened + "\n\n" + suffix
		}
	}
	if len(body) > caps.MaxBodyBytes {
		// Defensive: the construction above is bounded, so reaching this means
		// the planner and the declared capability disagree.
		return PublicationPlan{}, fmt.Errorf("planned body exceeds the forge limit of %d bytes", caps.MaxBodyBytes)
	}

	return PublicationPlan{
		WorkItem:     pub.WorkItem,
		Repository:   pub.Repository,
		BaseBranch:   pub.BaseBranch,
		Branch:       pub.Branch,
		Title:        title,
		Body:         body,
		Draft:        pub.Draft,
		Attachment:   attachment,
		StatusKind:   caps.StatusKind,
		BundleDigest: digest,
	}, nil
}

// normalizeSummary keeps valid UTF-8, normalizes line endings, and strips a NUL
// byte without treating untrusted summary text as markup or a shell string.
func normalizeSummary(summary string) string {
	summary = strings.ToValidUTF8(summary, "\uFFFD")
	summary = strings.ReplaceAll(summary, "\r\n", "\n")
	summary = strings.ReplaceAll(summary, "\r", "\n")
	summary = strings.ReplaceAll(summary, "\x00", "")
	return strings.TrimSpace(summary)
}

// truncateUTF8 returns at most max bytes of s without splitting a rune.
func truncateUTF8(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return strings.TrimSpace(s[:max])
}
