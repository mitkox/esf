package azuredevops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

var _ forge.Connector = (*Client)(nil)

const (
	// azureDevOpsMaxBodyBytes is the documented pull-request description cap.
	azureDevOpsMaxBodyBytes = 4000
	statusGenre             = "esf"
)

// Capabilities reports Azure DevOps publication features.
func (c *Client) Capabilities() forge.Capabilities {
	return forge.Capabilities{
		DraftPullRequests: true,
		MaxBodyBytes:      azureDevOpsMaxBodyBytes,
		StatusKind:        forge.StatusKindPullRequestStatus,
		Attachments:       true,
	}
}

// Report posts a work-item comment and, when a repository is supplied, a
// pull-request status. It never merges or approves anything.
func (c *Client) Report(ctx context.Context, req forge.ReportRequest) error {
	workItemID := strings.TrimSpace(req.WorkItem.ID)
	pullRequestID := strings.TrimSpace(req.PullRequestID)
	if workItemID == "" && pullRequestID == "" {
		return fmt.Errorf("azuredevops: report requires a work item or pull request")
	}

	// A comment is posted only when one was explicitly requested. The
	// pull-request status is independent: a missing project or a failed comment
	// must never suppress the verification status a branch policy requires.
	var errs []error
	comment := strings.TrimSpace(req.Comment)
	if workItemID != "" && comment != "" {
		project := strings.TrimSpace(req.WorkItem.Project)
		if project == "" {
			errs = append(errs, fmt.Errorf("azuredevops: work item project is required to post a comment"))
		} else {
			endpoint := c.apiURL(project, "_apis", "wit", "workItems", workItemID, "comments")
			query := endpoint.Query()
			query.Set("api-version", "7.0-preview.3")
			endpoint.RawQuery = query.Encode()
			payload, err := json.Marshal(map[string]string{"text": comment})
			if err != nil {
				errs = append(errs, err)
			} else if _, err := c.do(ctx, http.MethodPost, endpoint, payload, "application/json"); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if pullRequestID != "" {
		repository := strings.TrimSpace(req.Repository)
		if repository == "" {
			errs = append(errs, fmt.Errorf("azuredevops: repository is required to post a pull request status"))
		} else if project, repo, err := c.repositoryCoordinates(repository); err != nil {
			errs = append(errs, err)
		} else {
			endpoint := c.apiURL(project, "_apis", "git", "repositories", repo, "pullRequests", pullRequestID, "statuses")
			query := endpoint.Query()
			query.Set("api-version", apiVersion)
			endpoint.RawQuery = query.Encode()

			name := strings.TrimSpace(req.Status.Name)
			if name == "" {
				name = forge.VerificationStatusName
			}
			state := azureStatusState(req.Status.State)
			payload, err := json.Marshal(map[string]any{
				"state":       state,
				"description": truncateText(req.Status.Description, 1000),
				"context": map[string]string{
					"name":  name,
					"genre": statusGenre,
				},
				"targetUrl": req.Status.TargetURL,
			})
			if err != nil {
				errs = append(errs, err)
			} else if _, err := c.do(ctx, http.MethodPost, endpoint, payload, "application/json"); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// SubscriptionState reads the connector's Azure DevOps service-hook
// subscription state for connector health. It reports only a bounded summary,
// never a subscription body or credential.
func (c *Client) SubscriptionState(ctx context.Context) (forge.SubscriptionState, string, error) {
	endpoint := c.apiURL("_apis", "hooks", "subscriptions")
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	endpoint.RawQuery = query.Encode()
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return forge.SubscriptionUnknown, "subscription state unavailable", err
	}
	var response struct {
		Count int `json:"count"`
		Value []struct {
			Status string `json:"status"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return forge.SubscriptionUnknown, "subscription state unreadable", fmt.Errorf("azuredevops: decode service hook subscriptions: %w", err)
	}
	active, disabled, probationary, other := 0, 0, 0, 0
	for _, subscription := range response.Value {
		switch strings.ToLower(strings.TrimSpace(subscription.Status)) {
		case "enabled":
			active++
		case "disabled":
			disabled++
		case "probationary", "suspended", "errored":
			probationary++
		default:
			other++
		}
	}
	detail := fmt.Sprintf("%d subscription(s): %d enabled, %d disabled", len(response.Value), active, disabled)
	switch {
	case len(response.Value) == 0:
		return forge.SubscriptionUnknown, "no service-hook subscription is registered", nil
	case disabled > 0 && active == 0 && probationary == 0:
		return forge.SubscriptionDisabled, detail, nil
	case probationary > 0:
		return forge.SubscriptionProbationary, detail, nil
	case active > 0:
		return forge.SubscriptionActive, detail, nil
	default:
		return forge.SubscriptionUnknown, detail + fmt.Sprintf(", %d other", other), nil
	}
}

// Reconcile reads one page of the reporting revisions feed and re-fetches the
// newest revision of every work item in it.
func (c *Client) Reconcile(ctx context.Context, req forge.ReconcileRequest) (forge.ReconcileResult, error) {
	page, err := c.FetchRevisions(ctx, req.Cursor, req.Limit)
	if err != nil {
		return forge.ReconcileResult{}, err
	}
	latest := make(map[int64]int64)
	for _, revision := range page.Revisions {
		if previous, ok := latest[revision.WorkItemID]; !ok || revision.Revision > previous {
			latest[revision.WorkItemID] = revision.Revision
		}
	}
	ids := make([]int64, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if latest[ids[i]] != latest[ids[j]] {
			return latest[ids[i]] < latest[ids[j]]
		}
		return ids[i] < ids[j]
	})
	out := forge.ReconcileResult{Cursor: page.ContinuationToken}
	for _, id := range ids {
		snapshot, err := c.FetchWorkItem(ctx, id)
		if err != nil {
			return forge.ReconcileResult{}, err
		}
		out.WorkItems = append(out.WorkItems, snapshot.WorkItem)
	}
	return out, nil
}

type azureIdentity struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
	Descriptor  string `json:"descriptor"`
}

type azureCommitRef struct {
	CommitID string `json:"commitId"`
}

type azurePullRequest struct {
	PullRequestID         int64          `json:"pullRequestId"`
	Status                string         `json:"status"`
	MergeStatus           string         `json:"mergeStatus"`
	IsDraft               bool           `json:"isDraft"`
	URL                   string         `json:"url"`
	SourceRefName         string         `json:"sourceRefName"`
	TargetRefName         string         `json:"targetRefName"`
	CreatedBy             azureIdentity  `json:"createdBy"`
	ClosedBy              azureIdentity  `json:"closedBy"`
	AutoCompleteSetBy     azureIdentity  `json:"autoCompleteSetBy"`
	LastMergeCommit       azureCommitRef `json:"lastMergeCommit"`
	LastMergeSourceCommit azureCommitRef `json:"lastMergeSourceCommit"`
	LastMergeTargetCommit azureCommitRef `json:"lastMergeTargetCommit"`
	ClosedDate            string         `json:"closedDate"`
	Reviewers             []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		UniqueName  string `json:"uniqueName"`
		Descriptor  string `json:"descriptor"`
		Vote        int    `json:"vote"`
	} `json:"reviewers"`
	Repository struct {
		Name    string `json:"name"`
		WebURL  string `json:"webUrl"`
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
	} `json:"repository"`
}

// Publish opens (or finds) the deterministic pull request represented by a
// plan. The host publisher pushes the branch before this call; Azure DevOps
// requires the source ref to exist. Publication never merges or approves.
func (c *Client) Publish(ctx context.Context, plan forge.PublicationPlan) (forge.PublishResult, error) {
	project, repo, err := c.repositoryCoordinates(plan.Repository)
	if err != nil {
		return forge.PublishResult{}, err
	}
	title := strings.TrimSpace(plan.Title)
	body := strings.TrimSpace(plan.Body)
	if title == "" || body == "" {
		return forge.PublishResult{}, fmt.Errorf("azuredevops: pull request title and body are required")
	}
	if len(body) > c.Capabilities().MaxBodyBytes {
		return forge.PublishResult{}, fmt.Errorf("azuredevops: pull request body exceeds %d bytes", c.Capabilities().MaxBodyBytes)
	}
	sourceRef := "refs/heads/" + strings.TrimSpace(plan.Branch)
	targetRef := "refs/heads/" + strings.TrimSpace(plan.BaseBranch)
	if strings.TrimSpace(plan.Branch) == "" || strings.TrimSpace(plan.BaseBranch) == "" {
		return forge.PublishResult{}, fmt.Errorf("azuredevops: source and target branches are required")
	}

	existing, err := c.findActivePullRequest(ctx, project, repo, sourceRef)
	if err != nil {
		// Do not create a second pull request when the idempotency lookup
		// itself failed: publication must be retryable without duplicates.
		return forge.PublishResult{}, err
	}
	if existing != nil {
		if plan.Attachment != nil && c.Capabilities().Attachments {
			// Attaching by the fixed bundle name makes a retry update the same
			// attachment rather than creating a second pull request.
			if err := c.attach(ctx, project, repo, existing.PullRequestID, *plan.Attachment); err != nil {
				return forge.PublishResult{}, err
			}
		}
		return c.publishResult(existing, plan, project, repo), nil
	}

	endpoint := c.apiURL(project, "_apis", "git", "repositories", repo, "pullRequests")
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	endpoint.RawQuery = query.Encode()
	payload, err := json.Marshal(map[string]any{
		"sourceRefName": sourceRef,
		"targetRefName": targetRef,
		"title":         title,
		"description":   body,
		"isDraft":       plan.Draft,
	})
	if err != nil {
		return forge.PublishResult{}, err
	}
	response, err := c.do(ctx, http.MethodPost, endpoint, payload, "application/json")
	if err != nil {
		return forge.PublishResult{}, err
	}
	var created azurePullRequest
	if err := json.Unmarshal(response, &created); err != nil {
		return forge.PublishResult{}, fmt.Errorf("azuredevops: decode pull request: %w", err)
	}
	if created.PullRequestID <= 0 {
		return forge.PublishResult{}, fmt.Errorf("azuredevops: pull request response did not identify the pull request")
	}
	if plan.Attachment != nil && c.Capabilities().Attachments {
		if err := c.attach(ctx, project, repo, created.PullRequestID, *plan.Attachment); err != nil {
			return forge.PublishResult{}, err
		}
	}
	return c.publishResult(&created, plan, project, repo), nil
}

// ReadAcceptance reads the forge-native merge state. Azure DevOps completes a
// pull request only by merging it, so completed maps to merged and abandoned
// maps to closed.
func (c *Client) ReadAcceptance(ctx context.Context, req forge.AcceptanceRequest) (forge.AcceptanceResult, error) {
	project, repo, err := c.repositoryCoordinates(req.Repository)
	if err != nil {
		return forge.AcceptanceResult{}, err
	}
	pullRequestID := strings.TrimSpace(req.PullRequestID)
	if pullRequestID == "" {
		return forge.AcceptanceResult{}, fmt.Errorf("azuredevops: pull request id is required")
	}
	endpoint := c.apiURL(project, "_apis", "git", "repositories", repo, "pullRequests", pullRequestID)
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	endpoint.RawQuery = query.Encode()
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return forge.AcceptanceResult{}, err
	}
	var pullRequest azurePullRequest
	if err := json.Unmarshal(body, &pullRequest); err != nil {
		return forge.AcceptanceResult{}, fmt.Errorf("azuredevops: decode pull request: %w", err)
	}
	state, err := acceptanceState(pullRequest.Status)
	if err != nil {
		return forge.AcceptanceResult{}, err
	}
	result := forge.AcceptanceResult{
		PullRequest: forge.PullRequest{
			ID:     strconv.FormatInt(pullRequest.PullRequestID, 10),
			URL:    pullRequestURL(pullRequest, c.base, project, repo),
			Draft:  pullRequest.IsDraft,
			Commit: pullRequest.LastMergeCommit.CommitID,
		},
		State:             state,
		MergeCommit:       pullRequest.LastMergeCommit.CommitID,
		SourceCommit:      pullRequest.LastMergeSourceCommit.CommitID,
		AutoCompleteSetBy: identityName(pullRequest.AutoCompleteSetBy),
		ClosedBy:          identityName(pullRequest.ClosedBy),
	}
	if closedAt := strings.TrimSpace(pullRequest.ClosedDate); closedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, closedAt); err == nil {
			result.ClosedAt = parsed
		}
	}
	for _, reviewer := range pullRequest.Reviewers {
		result.Votes = append(result.Votes, forge.ApprovalVote{
			Reviewer:    firstNonEmpty(reviewer.UniqueName, reviewer.ID),
			DisplayName: reviewer.DisplayName,
			Descriptor:  reviewer.Descriptor,
			Vote:        reviewer.Vote,
		})
	}
	if state == forge.AcceptanceMerged && result.MergeCommit != "" {
		result.MergeTree = c.commitTree(ctx, project, repo, result.MergeCommit)
		result.PullRequest.Tree = result.MergeTree
	}
	return result, nil
}

func (c *Client) findActivePullRequest(ctx context.Context, project, repo, sourceRef string) (*azurePullRequest, error) {
	endpoint := c.apiURL(project, "_apis", "git", "repositories", repo, "pullRequests")
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	query.Set("searchCriteria.sourceRefName", sourceRef)
	query.Set("searchCriteria.status", "active")
	endpoint.RawQuery = query.Encode()
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var response struct {
		Value []azurePullRequest `json:"value"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("azuredevops: decode pull requests: %w", err)
	}
	if len(response.Value) == 0 {
		return nil, nil
	}
	existing := response.Value[0]
	return &existing, nil
}

func (c *Client) attach(ctx context.Context, project, repo string, pullRequestID int64, attachment forge.Attachment) error {
	name := strings.TrimSpace(attachment.Name)
	if name == "" || strings.ContainsAny(name, `/\`) || strings.ContainsAny(name, "\x00\r\n") {
		return fmt.Errorf("azuredevops: attachment name is invalid")
	}
	endpoint := c.apiURL(project, "_apis", "git", "repositories", repo, "pullRequests",
		strconv.FormatInt(pullRequestID, 10), "attachments", name)
	query := endpoint.Query()
	query.Set("api-version", "7.1-preview.1")
	endpoint.RawQuery = query.Encode()
	mediaType := strings.TrimSpace(attachment.MediaType)
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	_, err := c.do(ctx, http.MethodPost, endpoint, attachment.Data, mediaType)
	return err
}

func (c *Client) publishResult(pullRequest *azurePullRequest, plan forge.PublicationPlan, project, repo string) forge.PublishResult {
	return forge.PublishResult{PullRequest: forge.PullRequest{
		ID:    strconv.FormatInt(pullRequest.PullRequestID, 10),
		URL:   pullRequestURL(*pullRequest, c.base, project, repo),
		Draft: plan.Draft || pullRequest.IsDraft,
	}}
}

func pullRequestURL(pullRequest azurePullRequest, base *url.URL, project, repo string) string {
	if strings.TrimSpace(pullRequest.URL) != "" && strings.Contains(pullRequest.URL, "/pullrequest/") {
		return pullRequest.URL
	}
	if project == "" {
		project = pullRequest.Repository.Project.Name
	}
	if repo == "" {
		repo = pullRequest.Repository.Name
	}
	if base == nil || project == "" || repo == "" || pullRequest.PullRequestID <= 0 {
		return pullRequest.URL
	}
	path := strings.TrimRight(base.Path, "/") + "/" + project + "/_git/" + repo + "/pullrequest/" + strconv.FormatInt(pullRequest.PullRequestID, 10)
	return base.Scheme + "://" + base.Host + path
}

func acceptanceState(status string) (forge.AcceptanceState, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active":
		return forge.AcceptanceOpen, nil
	case "completed":
		return forge.AcceptanceMerged, nil
	case "abandoned":
		return forge.AcceptanceClosed, nil
	default:
		return "", fmt.Errorf("azuredevops: unsupported pull request status %q", status)
	}
}

func identityName(identity azureIdentity) string {
	return firstNonEmpty(identity.Descriptor, identity.UniqueName, identity.DisplayName, identity.ID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (c *Client) commitTree(ctx context.Context, project, repo, commit string) string {
	endpoint := c.apiURL(project, "_apis", "git", "repositories", repo, "commits", commit)
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	endpoint.RawQuery = query.Encode()
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return ""
	}
	var response struct {
		TreeID string `json:"treeId"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	return strings.TrimSpace(response.TreeID)
}

func azureStatusState(state forge.StatusState) string {
	switch state {
	case forge.StatusPending:
		return "pending"
	case forge.StatusSucceeded:
		return "succeeded"
	case forge.StatusFailed, forge.StatusError:
		return "failed"
	default:
		return "error"
	}
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && (value[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}
