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
