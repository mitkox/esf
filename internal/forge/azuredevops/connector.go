package azuredevops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mitkox/esf/internal/forge"
)

// The client implements the connector contract. Intake and reconciliation live
// in client.go, intake.go and webhook.go; this file completes the forge-facing
// operations with the same host-side credential and bounded retry rules.
var _ forge.Connector = (*Client)(nil)

const (
	// azureDevOpsMaxBodyBytes is the documented pull-request description cap.
	azureDevOpsMaxBodyBytes = 4000
	defaultStatusName       = "esf/verification"
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

	comment := strings.TrimSpace(req.Comment)
	if workItemID != "" && (comment != "" || req.Status.Description != "") {
		if comment == "" {
			comment = req.Status.Description
		}
		project := strings.TrimSpace(req.WorkItem.Project)
		if project == "" {
			return fmt.Errorf("azuredevops: work item project is required to post a comment")
		}
		endpoint := c.apiURL(project, "_apis", "wit", "workItems", workItemID, "comments")
		query := endpoint.Query()
		query.Set("api-version", "7.0-preview.3")
		endpoint.RawQuery = query.Encode()
		payload, err := json.Marshal(map[string]string{"text": comment})
		if err != nil {
			return err
		}
		if _, err := c.do(ctx, http.MethodPost, endpoint, payload, "application/json"); err != nil {
			return err
		}
	}

	if pullRequestID != "" {
		repository := strings.TrimSpace(req.Repository)
		if repository == "" {
			return fmt.Errorf("azuredevops: repository is required to post a pull request status")
		}
		project, repo, err := c.repositoryCoordinates(repository)
		if err != nil {
			return err
		}
		endpoint := c.apiURL(project, "_apis", "git", "repositories", repo, "pullRequests", pullRequestID, "statuses")
		query := endpoint.Query()
		query.Set("api-version", apiVersion)
		endpoint.RawQuery = query.Encode()

		name := strings.TrimSpace(req.Status.Name)
		if name == "" {
			name = defaultStatusName
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
			return err
		}
		if _, err := c.do(ctx, http.MethodPost, endpoint, payload, "application/json"); err != nil {
			return err
		}
	}
	return nil
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

type azurePullRequest struct {
	PullRequestID int64  `json:"pullRequestId"`
	Status        string `json:"status"`
	MergeStatus   string `json:"mergeStatus"`
	IsDraft       bool   `json:"isDraft"`
	URL           string `json:"url"`
	Repository    struct {
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
	return forge.AcceptanceResult{
		PullRequest: forge.PullRequest{
			ID:    strconv.FormatInt(pullRequest.PullRequestID, 10),
			URL:   pullRequestURL(pullRequest, c.base, project, repo),
			Draft: pullRequest.IsDraft,
		},
		State: state,
	}, nil
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
