package azuredevops

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/forge"
)

func TestClientCapabilitiesBoundPublication(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, "https://dev.azure.com/acme", RetryPolicy{MaxAttempts: 1})
	caps := client.Capabilities()
	if err := caps.Validate(); err != nil {
		t.Fatalf("Capabilities are not valid: %v", err)
	}
	if !caps.DraftPullRequests || !caps.Attachments {
		t.Fatalf("capabilities = %#v", caps)
	}
	if caps.MaxBodyBytes != azureDevOpsMaxBodyBytes {
		t.Fatalf("max body bytes = %d", caps.MaxBodyBytes)
	}
	if caps.StatusKind != forge.StatusKindPullRequestStatus {
		t.Fatalf("status kind = %q", caps.StatusKind)
	}
}

func TestPublishCreatesDraftPullRequest(t *testing.T) {
	t.Parallel()

	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		path := "/acme/Payments/_apis/git/repositories/payments/pullRequests"
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeFixture(w, []byte(`{"value":[]}`))
		case http.MethodPost:
			var payload struct {
				SourceRefName string `json:"sourceRefName"`
				TargetRefName string `json:"targetRefName"`
				Title         string `json:"title"`
				IsDraft       bool   `json:"isDraft"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode pull request: %v", err)
			}
			if payload.SourceRefName != "refs/heads/esf/work-item-42-r7" || payload.TargetRefName != "refs/heads/main" {
				t.Errorf("refs = %q -> %q", payload.SourceRefName, payload.TargetRefName)
			}
			if payload.Title != "Fix the retry loop" || !payload.IsDraft {
				t.Errorf("title/draft = %q/%v", payload.Title, payload.IsDraft)
			}
			writeFixture(w, []byte(`{"pullRequestId":17,"status":"active","isDraft":true}`))
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	result, err := client.Publish(context.Background(), forge.PublicationPlan{
		Repository: server.URL() + "/acme/Payments/_git/payments",
		BaseBranch: "main",
		Branch:     "esf/work-item-42-r7",
		Title:      "Fix the retry loop",
		Body:       "Summary.\n\nEvidence bundle digest: sha256:" + strings.Repeat("a", 64),
		Draft:      true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PullRequest.ID != "17" || !result.PullRequest.Draft {
		t.Fatalf("pull request = %#v", result.PullRequest)
	}
	if !strings.Contains(result.PullRequest.URL, "/acme/Payments/_git/payments/pullrequest/17") {
		t.Fatalf("pull request URL = %q", result.PullRequest.URL)
	}
}

func TestPublishReusesExistingPullRequest(t *testing.T) {
	t.Parallel()

	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Publish issued %s for an existing pull request", r.Method)
		}
		if r.URL.Query().Get("searchCriteria.sourceRefName") != "refs/heads/esf/work-item-42-r7" {
			t.Errorf("search query = %q", r.URL.RawQuery)
		}
		writeFixture(w, []byte(`{"value":[{"pullRequestId":17,"status":"active","isDraft":true}]}`))
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	result, err := client.Publish(context.Background(), forge.PublicationPlan{
		Repository: server.URL() + "/acme/Payments/_git/payments",
		BaseBranch: "main",
		Branch:     "esf/work-item-42-r7",
		Title:      "Fix the retry loop",
		Body:       "Summary.\n\nEvidence bundle digest: sha256:" + strings.Repeat("a", 64),
		Draft:      true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PullRequest.ID != "17" {
		t.Fatalf("pull request = %#v", result.PullRequest)
	}
}

func TestPublishReusesCompletedPullRequest(t *testing.T) {
	t.Parallel()

	var created bool
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/Payments/_apis/git/repositories/payments/pullRequests":
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("searchCriteria.sourceRefName") != "refs/heads/esf/work-item-42-r7" {
				t.Errorf("search query = %q", r.URL.RawQuery)
			}
			if r.URL.Query().Get("searchCriteria.status") == "active" {
				writeFixture(w, []byte(`{"value":[]}`))
				return
			}
			writeFixture(w, []byte(`{"value":[
				{"pullRequestId":16,"status":"abandoned","creationDate":"2026-11-08T09:00:00Z"},
				{"pullRequestId":17,"status":"completed","creationDate":"2026-11-08T11:00:00Z"}]}`))
		case http.MethodPost:
			created = true
			writeFixture(w, []byte(`{"pullRequestId":18,"status":"active"}`))
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	result, err := client.Publish(context.Background(), forge.PublicationPlan{
		Repository: server.URL() + "/acme/Payments/_git/payments",
		BaseBranch: "main",
		Branch:     "esf/work-item-42-r7",
		Title:      "Fix the retry loop",
		Body:       "Summary.\n\nEvidence bundle digest: sha256:" + strings.Repeat("a", 64),
		Draft:      true,
		Attachment: &forge.Attachment{Name: forge.EvidenceBundleAttachmentName, MediaType: "application/octet-stream"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if created {
		t.Fatal("a retry after a completed pull request created a second pull request")
	}
	if result.PullRequest.ID != "17" {
		t.Fatalf("pull request = %#v, want the completed pull request", result.PullRequest)
	}
	if !strings.Contains(result.PullRequest.URL, "/pullrequest/17") {
		t.Fatalf("pull request URL = %q", result.PullRequest.URL)
	}
}

func TestPublishReusesAbandonedPullRequest(t *testing.T) {
	t.Parallel()

	var created bool
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/Payments/_apis/git/repositories/payments/pullRequests" {
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("searchCriteria.status") == "active" {
				writeFixture(w, []byte(`{"value":[]}`))
				return
			}
			writeFixture(w, []byte(`{"value":[{"pullRequestId":17,"status":"abandoned","creationDate":"2026-11-08T11:00:00Z"}]}`))
		case http.MethodPost:
			created = true
			writeFixture(w, []byte(`{"pullRequestId":18,"status":"active"}`))
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	result, err := client.Publish(context.Background(), forge.PublicationPlan{
		Repository: server.URL() + "/acme/Payments/_git/payments",
		BaseBranch: "main",
		Branch:     "esf/work-item-42-r7",
		Title:      "Fix the retry loop",
		Body:       "Summary.\n\nEvidence bundle digest: sha256:" + strings.Repeat("a", 64),
		Draft:      true,
		Attachment: &forge.Attachment{Name: forge.EvidenceBundleAttachmentName, MediaType: "application/octet-stream"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if created {
		t.Fatal("a retry after an abandoned pull request created a second pull request")
	}
	if result.PullRequest.ID != "17" {
		t.Fatalf("pull request = %#v, want the abandoned pull request", result.PullRequest)
	}
}

func TestReadAcceptanceMapsForgeState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status string
		want   forge.AcceptanceState
	}{
		{status: "active", want: forge.AcceptanceOpen},
		{status: "completed", want: forge.AcceptanceMerged},
		{status: "abandoned", want: forge.AcceptanceClosed},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.status, func(t *testing.T) {
			t.Parallel()
			server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/acme/Payments/_apis/git/repositories/payments/pullRequests/17" {
					http.NotFound(w, r)
					return
				}
				writeFixture(w, []byte(`{"pullRequestId":17,"status":"`+tc.status+`"}`))
			})
			client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
			result, err := client.ReadAcceptance(context.Background(), forge.AcceptanceRequest{
				Repository:    server.URL() + "/acme/Payments/_git/payments",
				PullRequestID: "17",
			})
			if err != nil {
				t.Fatalf("ReadAcceptance: %v", err)
			}
			if result.State != tc.want {
				t.Fatalf("state = %q, want %q", result.State, tc.want)
			}
		})
	}
}

func TestReportPostsCommentAndPullRequestStatus(t *testing.T) {
	t.Parallel()

	var commentPath, statusPath string
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_apis/wit/workItems/42/comments"):
			commentPath = r.URL.Path
		case strings.HasSuffix(r.URL.Path, "/pullRequests/17/statuses"):
			statusPath = r.URL.Path
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	err := client.Report(context.Background(), forge.ReportRequest{
		WorkItem:      forge.WorkItemRef{ID: "42", Project: "Payments"},
		Repository:    server.URL() + "/acme/Payments/_git/payments",
		PullRequestID: "17",
		Status: forge.Status{
			Name:        "esf/verification",
			State:       forge.StatusSucceeded,
			Description: "verified",
		},
		Comment: "Evidence bundle digest verified.",
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if commentPath == "" || statusPath == "" {
		t.Fatalf("comment path = %q, status path = %q", commentPath, statusPath)
	}
}

func TestReportPostsPullRequestStatusWithoutCommentOrProject(t *testing.T) {
	t.Parallel()

	var statusPosted bool
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pullRequests/17/statuses") && r.Method == http.MethodPost {
			statusPosted = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	err := client.Report(context.Background(), forge.ReportRequest{
		// An older manifest carries no project. The status must still post.
		WorkItem:      forge.WorkItemRef{ID: "42"},
		Repository:    server.URL() + "/acme/Payments/_git/payments",
		PullRequestID: "17",
		Status: forge.Status{
			Name:        forge.VerificationStatusName,
			State:       forge.StatusSucceeded,
			Description: "verified",
		},
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !statusPosted {
		t.Fatal("the verification status was not posted")
	}
	if count := server.CountPath("/acme/_apis/wit/workItems/42/comments"); count != 0 {
		t.Fatalf("comment posted without an explicit comment: %d", count)
	}
}

func TestReportPostsStatusWhenCommentProjectIsMissing(t *testing.T) {
	t.Parallel()

	var statusPosted bool
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pullRequests/17/statuses") && r.Method == http.MethodPost {
			statusPosted = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		http.NotFound(w, r)
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	err := client.Report(context.Background(), forge.ReportRequest{
		WorkItem:      forge.WorkItemRef{ID: "42"},
		Repository:    server.URL() + "/acme/Payments/_git/payments",
		PullRequestID: "17",
		Comment:       "Evidence verified.",
		Status:        forge.Status{Name: forge.VerificationStatusName, State: forge.StatusSucceeded},
	})
	if err == nil || !strings.Contains(err.Error(), "project is required") {
		t.Fatalf("Report error = %v, want the missing-project comment error", err)
	}
	if !statusPosted {
		t.Fatal("a failed comment suppressed the pull-request status")
	}
}

func TestSubscriptionStateMapsServiceHooks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		state  forge.SubscriptionState
		detail string
	}{
		{name: "active", body: `{"count":1,"value":[{"status":"enabled"}]}`, state: forge.SubscriptionActive},
		{name: "disabled", body: `{"count":1,"value":[{"status":"disabled"}]}`, state: forge.SubscriptionDisabled},
		{name: "probationary", body: `{"count":1,"value":[{"status":"suspended"}]}`, state: forge.SubscriptionProbationary},
		{name: "empty", body: `{"count":0,"value":[]}`, state: forge.SubscriptionUnknown},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/acme/_apis/hooks/subscriptions" {
					http.NotFound(w, r)
					return
				}
				writeFixture(w, []byte(tc.body))
			})
			client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
			state, detail, err := client.SubscriptionState(context.Background())
			if err != nil {
				t.Fatalf("SubscriptionState: %v", err)
			}
			if state != tc.state {
				t.Fatalf("state = %q, want %q", state, tc.state)
			}
			if strings.TrimSpace(detail) == "" {
				t.Fatal("subscription detail is empty")
			}
		})
	}
}

func TestPublishEscapesProjectAndRepositoryPathSegments(t *testing.T) {
	t.Parallel()

	const project = "Payments Team"
	const repo = "payments repo"
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/acme/"+project+"/_apis/git/repositories/"+repo+"/pullRequests" {
				t.Errorf("decoded request path = %q", r.URL.Path)
			}
			writeFixture(w, []byte(`{"value":[]}`))
		case http.MethodPost:
			if r.URL.Path != "/acme/"+project+"/_apis/git/repositories/"+repo+"/pullRequests" {
				t.Errorf("decoded request path = %q", r.URL.Path)
			}
			writeFixture(w, []byte(`{"pullRequestId":17,"status":"active","isDraft":true}`))
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	result, err := client.Publish(context.Background(), forge.PublicationPlan{
		Repository: server.URL() + "/acme/" + project + "/_git/" + repo,
		BaseBranch: "main",
		Branch:     "esf/work-item-42-r7",
		Title:      "Fix the retry loop",
		Body:       "Summary.\n\nEvidence bundle digest: sha256:" + strings.Repeat("a", 64),
		Draft:      true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	wantURL := server.URL() + "/acme/Payments%20Team/_git/payments%20repo/pullrequest/17"
	if result.PullRequest.URL != wantURL {
		t.Fatalf("pull request URL = %q, want %q", result.PullRequest.URL, wantURL)
	}
	wantPath := "/acme/Payments%20Team/_apis/git/repositories/payments%20repo/pullRequests?"
	for _, request := range server.Requests() {
		if !strings.HasPrefix(request.RequestURI, wantPath) {
			t.Fatalf("%s request URI = %q, want prefix %q", request.Method, request.RequestURI, wantPath)
		}
	}
}
