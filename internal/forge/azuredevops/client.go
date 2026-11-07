// Package azuredevops implements the Azure DevOps connector's work-item intake
// and reconciliation paths.
//
// The connector is host-side only. Its credential is read from a private file
// and is never placed in a Cube sandbox environment or in evidence. Sandbox
// git access is declared separately as an egress header-injection rule so the
// Cube egress proxy can add the credential at the boundary.
package azuredevops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

const (
	apiVersion               = "7.0"
	defaultPageSize          = 100
	maxPageSize              = 200
	maxResponseBytes         = 8 << 20
	gitRepositoriesNamespace = "2e9eb7ed-3c0a-47d4-87c1-0ffdd275fd87"
	gitContributePermission  = 4
)

// Config configures an Azure DevOps connector client.
type Config struct {
	// Name is the operator-facing connector name used in intake records.
	Name string
	// BaseURL is the organization root, for example
	// https://dev.azure.com/acme. It must not include a query or fragment.
	BaseURL string
	// Credential is the private-file credential. Its value is treated as
	// "username:password"; a bare value is used as the password with an empty
	// username, which Azure DevOps PAT authentication accepts.
	Credential forge.Credential
	// Clone declares the private-file credential and git endpoints a sandbox
	// clone needs at the Cube egress boundary.
	Clone CloneCredential
	// HTTPClient is optional; tests inject the fake server's client.
	HTTPClient *http.Client
	// Retry controls 429 handling. Zero values use bounded defaults.
	Retry RetryPolicy
}

// RetryPolicy bounds retries of a throttled forge request.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts, including the first.
	MaxAttempts int
	// BaseDelay is the initial exponential backoff delay.
	BaseDelay time.Duration
	// MaxDelay caps the exponential backoff (Retry-After is honored as given).
	MaxDelay time.Duration
	// Sleep is injectable for deterministic tests. Nil uses a context-aware
	// timer.
	Sleep func(ctx context.Context, delay time.Duration) error
}

// Client is a host-side Azure DevOps API client.
type Client struct {
	name       string
	base       *url.URL
	credential forge.Credential
	http       *http.Client
	retry      RetryPolicy
	clone      CloneCredential
}

// NewClient validates and constructs an Azure DevOps client.
//
// The credential file has already been read through the private-file rules by
// the configuration layer. Construction reads no clone secret.
func NewClient(cfg Config) (*Client, error) {
	base, err := parseBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	_, password := splitBasicCredential(cfg.Credential.Value())
	if password == "" {
		return nil, fmt.Errorf("azuredevops: connector credential is empty")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	retry := cfg.Retry
	if retry.MaxAttempts <= 0 {
		retry.MaxAttempts = 3
	}
	if retry.MaxAttempts > 10 {
		retry.MaxAttempts = 10
	}
	if retry.BaseDelay <= 0 {
		retry.BaseDelay = 200 * time.Millisecond
	}
	if retry.MaxDelay <= 0 {
		retry.MaxDelay = 5 * time.Second
	}
	return &Client{
		name:       strings.TrimSpace(cfg.Name),
		base:       base,
		credential: cfg.Credential,
		http:       httpClient,
		retry:      retry,
		clone:      cfg.Clone,
	}, nil
}

// Name returns the operator-facing connector name.
func (c *Client) Name() string { return c.name }

// String redacts the client's private credential.
func (c *Client) String() string { return "azuredevops.Client([redacted])" }

// GoString redacts the client's private credential in %#v formatting.
func (c *Client) GoString() string { return "azuredevops.Client([redacted])" }

// WorkItemRevision is one entry from the reporting revisions feed.
type WorkItemRevision struct {
	WorkItemID int64
	Revision   int64
}

// RevisionPage is one bounded page of the reporting revisions feed.
type RevisionPage struct {
	Revisions         []WorkItemRevision
	ContinuationToken string
	IsLastBatch       bool
}

// WorkItemSnapshot is a re-fetched work item and the digest of its exact bytes.
type WorkItemSnapshot struct {
	WorkItem     forge.WorkItem
	WorkItemHash string
	Actor        forge.Actor
}

// FetchRevisions reads one page of the reporting work-item revisions feed.
//
// The continuation token is the feed watermark. A limit above the connector's
// bounded page size is clamped rather than rejected.
func (c *Client) FetchRevisions(ctx context.Context, continuationToken string, limit int) (RevisionPage, error) {
	if limit <= 0 {
		limit = defaultPageSize
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}
	endpoint := c.apiURL("_apis", "wit", "reporting", "workitemrevisions")
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	query.Set("$maxPageSize", strconv.Itoa(limit))
	if token := strings.TrimSpace(continuationToken); token != "" {
		query.Set("continuationToken", token)
	}
	endpoint.RawQuery = query.Encode()

	body, err := c.get(ctx, endpoint)
	if err != nil {
		return RevisionPage{}, err
	}
	var response struct {
		Values []struct {
			ID  int64 `json:"id"`
			Rev int64 `json:"rev"`
		} `json:"values"`
		ContinuationToken string `json:"continuationToken"`
		IsLastBatch       bool   `json:"isLastBatch"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return RevisionPage{}, fmt.Errorf("azuredevops: decode work item revisions: %w", err)
	}
	page := RevisionPage{
		Revisions:         make([]WorkItemRevision, 0, len(response.Values)),
		ContinuationToken: strings.TrimSpace(response.ContinuationToken),
		IsLastBatch:       response.IsLastBatch,
	}
	for _, value := range response.Values {
		if value.ID <= 0 || value.Rev <= 0 {
			return RevisionPage{}, fmt.Errorf("azuredevops: revisions feed returned an invalid work item id or revision")
		}
		page.Revisions = append(page.Revisions, WorkItemRevision{WorkItemID: value.ID, Revision: value.Rev})
	}
	return page, nil
}

// FetchWorkItem re-fetches one work item by ID and digests the exact response
// bytes. The returned title and body are untrusted work-item text.
func (c *Client) FetchWorkItem(ctx context.Context, id int64) (WorkItemSnapshot, error) {
	if id <= 0 {
		return WorkItemSnapshot{}, fmt.Errorf("azuredevops: work item id must be positive")
	}
	endpoint := c.apiURL("_apis", "wit", "workitems", strconv.FormatInt(id, 10))
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	query.Set("$expand", "all")
	endpoint.RawQuery = query.Encode()

	raw, err := c.get(ctx, endpoint)
	if err != nil {
		return WorkItemSnapshot{}, err
	}
	snapshot, err := parseWorkItem(raw)
	if err != nil {
		return WorkItemSnapshot{}, err
	}
	if snapshot.WorkItem.ID != strconv.FormatInt(id, 10) {
		return WorkItemSnapshot{}, fmt.Errorf("azuredevops: work item %d response identified work item %q", id, snapshot.WorkItem.ID)
	}
	snapshot.WorkItemHash = forge.DigestWorkItem(raw)
	return snapshot, nil
}

// RepositoryWrite reports whether the actor descriptor has the Git Contribute
// permission on the repository.
//
// The repository must be an Azure DevOps git URL under this connector's base
// URL. Permission is evaluated at repository level; when no repository ACE is
// returned, the project-level token is checked for inherited permission.
func (c *Client) RepositoryWrite(ctx context.Context, repository, descriptor string) (bool, error) {
	descriptor = strings.TrimSpace(descriptor)
	if descriptor == "" || strings.ContainsAny(descriptor, "\x00\r\n") {
		return false, fmt.Errorf("azuredevops: actor descriptor is required")
	}
	project, repo, err := c.repositoryCoordinates(repository)
	if err != nil {
		return false, err
	}

	endpoint := c.apiURL(project, "_apis", "git", "repositories", repo)
	query := endpoint.Query()
	query.Set("api-version", apiVersion)
	endpoint.RawQuery = query.Encode()
	body, err := c.get(ctx, endpoint)
	if err != nil {
		return false, err
	}
	var repositoryInfo struct {
		ID      string `json:"id"`
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
	}
	if err := json.Unmarshal(body, &repositoryInfo); err != nil {
		return false, fmt.Errorf("azuredevops: decode repository: %w", err)
	}
	if strings.TrimSpace(repositoryInfo.ID) == "" || strings.TrimSpace(repositoryInfo.Project.ID) == "" {
		return false, fmt.Errorf("azuredevops: repository response did not identify the repository and project")
	}

	repositoryToken := "repoV2/" + repositoryInfo.Project.ID + "/" + repositoryInfo.ID
	granted, found, err := c.aclWrite(ctx, repositoryToken, descriptor)
	if err != nil {
		return false, err
	}
	if found {
		return granted, nil
	}
	granted, _, err = c.aclWrite(ctx, "repoV2/"+repositoryInfo.Project.ID, descriptor)
	if err != nil {
		return false, err
	}
	return granted, nil
}

// aclWrite reads one ACL token and reports whether the descriptor's effective
// Contribute bit is allowed and not denied.
func (c *Client) aclWrite(ctx context.Context, token, descriptor string) (granted, found bool, err error) {
	endpoint := c.apiURL("_apis", "accesscontrollists", gitRepositoriesNamespace)
	query := endpoint.Query()
	query.Set("token", token)
	query.Set("descriptors", descriptor)
	query.Set("includeExtendedInfo", "true")
	query.Set("api-version", apiVersion)
	endpoint.RawQuery = query.Encode()

	body, err := c.get(ctx, endpoint)
	if err != nil {
		return false, false, err
	}
	var response struct {
		Value []struct {
			ACEs map[string]struct {
				Descriptor string `json:"descriptor"`
				Allow      int64  `json:"allow"`
				Deny       int64  `json:"deny"`
				Extended   *struct {
					EffectiveAllow int64 `json:"effectiveAllow"`
					EffectiveDeny  int64 `json:"effectiveDeny"`
				} `json:"extendedInfo"`
			} `json:"acesDictionary"`
		} `json:"value"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return false, false, fmt.Errorf("azuredevops: decode access control list: %w", err)
	}
	for _, list := range response.Value {
		for key, ace := range list.ACEs {
			if key != descriptor && ace.Descriptor != descriptor {
				continue
			}
			allow, deny := ace.Allow, ace.Deny
			if ace.Extended != nil {
				allow, deny = ace.Extended.EffectiveAllow, ace.Extended.EffectiveDeny
			}
			return allow&gitContributePermission != 0 && deny&gitContributePermission == 0, true, nil
		}
	}
	return false, false, nil
}

// repositoryCoordinates extracts the project and repository names from an
// operator-configured git URL under the connector's base URL.
func (c *Client) repositoryCoordinates(repository string) (project, repo string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(repository))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("azuredevops: repository %q is not an absolute git URL", repository)
	}
	if !strings.EqualFold(parsed.Scheme, c.base.Scheme) || !strings.EqualFold(parsed.Host, c.base.Host) {
		return "", "", fmt.Errorf("azuredevops: repository %q is outside the connector base URL", repository)
	}
	path := strings.Trim(parsed.Path, "/")
	basePath := strings.TrimPrefix(strings.TrimRight(c.base.Path, "/"), "/")
	if basePath != "" {
		if path == basePath || !strings.HasPrefix(path, basePath+"/") {
			return "", "", fmt.Errorf("azuredevops: repository %q is outside the connector organization", repository)
		}
		path = strings.TrimPrefix(path, basePath+"/")
	}
	segments := strings.Split(path, "/")
	if len(segments) != 3 || segments[1] != "_git" || segments[0] == "" || segments[2] == "" {
		return "", "", fmt.Errorf("azuredevops: repository %q is not an Azure DevOps git URL", repository)
	}
	project, repo = segments[0], segments[2]
	if strings.ContainsAny(project+repo, "\x00\r\n\t") {
		return "", "", fmt.Errorf("azuredevops: repository %q contains control characters", repository)
	}
	return project, repo, nil
}

// get performs a bounded, authenticated GET with 429 backoff.
func (c *Client) get(ctx context.Context, endpoint *url.URL) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < c.retry.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("azuredevops: build request: %w", err)
		}
		username, password := splitBasicCredential(c.credential.Value())
		request.SetBasicAuth(username, password)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "esf-azuredevops-connector")

		response, err := c.http.Do(request)
		if err != nil {
			return nil, fmt.Errorf("azuredevops: request %s: %w", endpoint.Path, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("azuredevops: read response: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("azuredevops: close response: %w", closeErr)
		}
		if len(body) > maxResponseBytes {
			return nil, fmt.Errorf("azuredevops: response exceeds %d bytes", maxResponseBytes)
		}

		if response.StatusCode == http.StatusTooManyRequests {
			lastErr = apiErrorFromResponse(response.StatusCode, endpoint.Path, body)
			if attempt == c.retry.MaxAttempts-1 {
				return nil, lastErr
			}
			delay := c.retryDelay(response, attempt)
			if err := c.sleep(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return nil, apiErrorFromResponse(response.StatusCode, endpoint.Path, body)
		}
		return body, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("azuredevops: request %s exhausted retries", endpoint.Path)
}

func (c *Client) retryDelay(response *http.Response, attempt int) time.Duration {
	delay := c.retry.BaseDelay
	for i := 0; i < attempt; i++ {
		if delay >= c.retry.MaxDelay/2 {
			delay = c.retry.MaxDelay
			break
		}
		delay *= 2
	}
	if delay > c.retry.MaxDelay {
		delay = c.retry.MaxDelay
	}
	if retryAfter := parseRetryAfter(response.Header.Get("Retry-After")); retryAfter > delay {
		delay = retryAfter
	}
	return delay
}

func (c *Client) sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if c.retry.Sleep != nil {
		return c.retry.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// APIError is a non-success Azure DevOps response. It never carries request or
// credential data.
type APIError struct {
	StatusCode int
	Path       string
	Message    string
}

// Error reports the status and the bounded forge message.
func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("azuredevops: %s returned HTTP %d", e.Path, e.StatusCode)
	}
	return fmt.Sprintf("azuredevops: %s returned HTTP %d: %s", e.Path, e.StatusCode, e.Message)
}

func apiErrorFromResponse(status int, path string, body []byte) *APIError {
	message := ""
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &payload) == nil {
		message = strings.TrimSpace(payload.Message)
	}
	message = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, message)
	if len(message) > 256 {
		message = message[:256]
	}
	return &APIError{StatusCode: status, Path: path, Message: message}
}

func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

func parseBaseURL(raw string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || base.Scheme == "" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("azuredevops: base URL must be an absolute URL without userinfo, query, or fragment")
	}
	switch strings.ToLower(base.Scheme) {
	case "https":
	case "http":
		host := base.Hostname()
		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("azuredevops: base URL must use HTTPS or HTTP on a loopback host")
		}
	default:
		return nil, fmt.Errorf("azuredevops: base URL must use HTTPS")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawPath = ""
	return base, nil
}

func (c *Client) apiURL(segments ...string) *url.URL {
	endpoint := *c.base
	path := strings.TrimRight(endpoint.Path, "/")
	for _, segment := range segments {
		path += "/" + segment
	}
	endpoint.Path = path
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return &endpoint
}

func splitBasicCredential(value string) (username, password string) {
	if user, pass, ok := strings.Cut(value, ":"); ok {
		return user, pass
	}
	return "", value
}

func rawString(fields map[string]json.RawMessage, name string) string {
	raw, ok := fields[name]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func rawTags(fields map[string]json.RawMessage, name string) []string {
	value := rawString(fields, name)
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ";")
	tags := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func parseWorkItem(raw []byte) (WorkItemSnapshot, error) {
	var response struct {
		ID     int64                      `json:"id"`
		Rev    int64                      `json:"rev"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return WorkItemSnapshot{}, fmt.Errorf("azuredevops: decode work item: %w", err)
	}
	if response.ID <= 0 || response.Rev <= 0 {
		return WorkItemSnapshot{}, fmt.Errorf("azuredevops: work item response is missing its id or revision")
	}
	workItem := forge.WorkItem{
		ID:       strconv.FormatInt(response.ID, 10),
		Revision: strconv.FormatInt(response.Rev, 10),
		WorkItemFields: forge.WorkItemFields{
			Project: rawString(response.Fields, "System.TeamProject"),
			Area:    rawString(response.Fields, "System.AreaPath"),
			Type:    rawString(response.Fields, "System.WorkItemType"),
			State:   rawString(response.Fields, "System.State"),
			Labels:  rawTags(response.Fields, "System.Tags"),
		},
		Title: rawString(response.Fields, "System.Title"),
		Body:  rawString(response.Fields, "System.Description"),
	}
	actor := parseActor(response.Fields, "System.ChangedBy")
	if actor.Descriptor == "" && actor.ID == "" {
		actor = parseActor(response.Fields, "System.CreatedBy")
	}
	return WorkItemSnapshot{WorkItem: workItem, Actor: actor}, nil
}

func parseActor(fields map[string]json.RawMessage, name string) forge.Actor {
	raw, ok := fields[name]
	if !ok {
		return forge.Actor{}
	}
	var identity struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		UniqueName  string `json:"uniqueName"`
		Descriptor  string `json:"descriptor"`
	}
	if json.Unmarshal(raw, &identity) != nil {
		return forge.Actor{}
	}
	return forge.Actor{
		ID:          identity.ID,
		DisplayName: identity.DisplayName,
		UniqueName:  identity.UniqueName,
		Descriptor:  identity.Descriptor,
	}
}

// IsThrottled reports whether err is an Azure DevOps 429 response.
func IsThrottled(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests
}
