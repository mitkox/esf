package forge

import "fmt"

// Capabilities describes the forge features a connector can rely on.
//
// The flags are intentionally per-connector rather than a shared minimum: a
// forge that cannot attach a file, or that caps a pull-request body, must say so
// instead of silently failing when the factory publishes a change.
type Capabilities struct {
	// DraftPullRequests reports whether a pull request can be opened as a draft.
	DraftPullRequests bool
	// MaxBodyBytes is the forge's maximum pull-request body size in bytes.
	MaxBodyBytes int
	// StatusKind selects the forge-native surface used to report a status.
	StatusKind StatusKind
	// Attachments reports whether a file can be attached to a pull request.
	Attachments bool
}

// StatusKind identifies the forge-native status surface.
type StatusKind string

const (
	// StatusKindCommitStatus posts a status against a commit.
	StatusKindCommitStatus StatusKind = "commit_status"
	// StatusKindCheckRun posts a check run.
	StatusKindCheckRun StatusKind = "check_run"
	// StatusKindPullRequestStatus posts a status against a pull request.
	StatusKindPullRequestStatus StatusKind = "pull_request_status"
)

// Valid reports whether the status kind is understood by the factory.
func (k StatusKind) Valid() bool {
	switch k {
	case StatusKindCommitStatus, StatusKindCheckRun, StatusKindPullRequestStatus:
		return true
	default:
		return false
	}
}

// Validate rejects a capability set that cannot produce a bounded publication.
func (c Capabilities) Validate() error {
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("forge maximum body bytes must be positive")
	}
	if !c.StatusKind.Valid() {
		return fmt.Errorf("forge status kind %q is not supported", c.StatusKind)
	}
	return nil
}
