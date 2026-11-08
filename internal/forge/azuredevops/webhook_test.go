package azuredevops

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mitkox/esf/internal/forge"
)

type captureSink struct {
	mu         sync.Mutex
	proposals  []forge.Proposal
	rejections []forge.Rejection
}

func (s *captureSink) Propose(_ context.Context, proposal forge.Proposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proposals = append(s.proposals, proposal)
	return nil
}

func (s *captureSink) Reject(_ context.Context, rejection forge.Rejection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejections = append(s.rejections, rejection)
	return nil
}

func (s *captureSink) snapshot() ([]forge.Proposal, []forge.Rejection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]forge.Proposal(nil), s.proposals...), append([]forge.Rejection(nil), s.rejections...)
}

const (
	webhookTestUsername = "esf-hooks"
	webhookTestPassword = "hook-secret"
)

func webhookTestSecret(t *testing.T) forge.Credential {
	t.Helper()
	credential, err := forge.ReadCredential(writeCredentialFile(t, webhookTestPassword))
	if err != nil {
		t.Fatalf("read webhook credential: %v", err)
	}
	return credential
}

func newWebhookHarness(t *testing.T, aclFixture string, mutate ...func(*WebhookConfig)) (*forgeServer, *captureSink, *Webhook) {
	t.Helper()
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/_apis/wit/workitems/42":
			writeFixture(w, readFixture(t, "workitem-42.json"))
		case "/acme/Payments/_apis/git/repositories/payments":
			writeFixture(w, readFixture(t, "repository.json"))
		case "/acme/_apis/accesscontrollists/" + gitRepositoriesNamespace:
			if strings.HasSuffix(r.URL.Query().Get("token"), "/repo-payments") {
				writeFixture(w, readFixture(t, aclFixture))
				return
			}
			writeFixture(w, readFixture(t, "acl-empty.json"))
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	sink := &captureSink{}
	cfg := WebhookConfig{
		Connector: "azdo",
		Username:  webhookTestUsername,
		Secret:    webhookTestSecret(t),
		Sink:      sink,
		Repositories: forge.RepositoryResolverFunc(func(forge.WorkItemFields) (string, error) {
			return server.URL() + "/acme/Payments/_git/payments", nil
		}),
		WatchedFields: []string{"System.State"},
	}
	for _, apply := range mutate {
		apply(&cfg)
	}
	webhook, err := NewWebhook(newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1}), cfg)
	if err != nil {
		t.Fatalf("NewWebhook: %v", err)
	}
	return server, sink, webhook
}

func postWebhook(t *testing.T, webhook *Webhook, body []byte, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://factory.local/hooks/azuredevops", bytes.NewReader(body))
	request.SetBasicAuth(username, password)
	recorder := httptest.NewRecorder()
	webhook.ServeHTTP(recorder, request)
	return recorder
}

func TestWebhookAcceptsMappedUpdateFromReFetchedWorkItem(t *testing.T) {
	t.Parallel()

	server, sink, webhook := newWebhookHarness(t, "acl-allow.json")
	response := postWebhook(t, webhook, readFixture(t, "webhook-updated-mapped.json"), webhookTestUsername, webhookTestPassword)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", response.Code)
	}

	proposals, rejections := sink.snapshot()
	if len(proposals) != 1 || len(rejections) != 0 {
		t.Fatalf("proposals = %d, rejections = %d", len(proposals), len(rejections))
	}
	proposal := proposals[0]
	if proposal.Source != forge.SourceWebhook || proposal.Connector != "azdo" || proposal.DeliveryID != "event-0001" {
		t.Fatalf("proposal identity = %#v", proposal)
	}
	if proposal.WorkItem.ID != "42" || proposal.WorkItem.Revision != "6" {
		t.Fatalf("work item = %s rev %s", proposal.WorkItem.ID, proposal.WorkItem.Revision)
	}
	if proposal.WorkItem.Title != "Payment retry loop spins forever" {
		t.Fatalf("proposal used delivery text instead of the re-fetched work item: %q", proposal.WorkItem.Title)
	}
	if proposal.WorkItem.Project != "Payments" || proposal.WorkItem.Area != "Payments\\Bugs" || proposal.WorkItem.Type != "Bug" || proposal.WorkItem.State != "Active" {
		t.Fatalf("work item fields = %#v", proposal.WorkItem.WorkItemFields)
	}
	if len(proposal.WorkItem.Labels) != 2 || proposal.WorkItem.Labels[0] != "esf" || proposal.WorkItem.Labels[1] != "repro" {
		t.Fatalf("labels = %#v", proposal.WorkItem.Labels)
	}
	if proposal.Actor.DisplayName != "Ada Lovelace" || proposal.Actor.Descriptor != "aad.ada-lovelace" {
		t.Fatalf("actor = %#v, want the API-resolved actor", proposal.Actor)
	}
	if proposal.RepositoryPermission != forge.PermissionWrite {
		t.Fatalf("repository permission = %q, want write", proposal.RepositoryPermission)
	}
	if proposal.WorkItemHash != forge.DigestWorkItem(readFixture(t, "workitem-42.json")) {
		t.Fatalf("work item hash = %q", proposal.WorkItemHash)
	}
	if len(proposal.ChangedFields) != 1 || proposal.ChangedFields[0] != "System.State" {
		t.Fatalf("changed fields = %#v", proposal.ChangedFields)
	}
	if stats := webhook.Stats(); stats.Accepted != 1 || stats.Rejected != 0 {
		t.Fatalf("stats = %#v", stats)
	}

	requests := server.Requests()
	if server.CountPath("/acme/_apis/wit/workitems/42") != 1 {
		t.Fatalf("work item fetch count = %d", server.CountPath("/acme/_apis/wit/workitems/42"))
	}
	var aclRequest forgeRequest
	for _, request := range requests {
		if strings.HasPrefix(request.Path, "/acme/_apis/accesscontrollists/") {
			aclRequest = request
		}
	}
	if aclRequest.Path == "" {
		t.Fatal("the actor permission check did not query the ACL API")
	}
	if aclRequest.Query.Get("token") != "repoV2/project-payments/repo-payments" {
		t.Fatalf("ACL token = %q", aclRequest.Query.Get("token"))
	}
	if aclRequest.Query.Get("descriptors") != "aad.ada-lovelace" || aclRequest.Query.Get("includeExtendedInfo") != "true" {
		t.Fatalf("ACL query = %#v", aclRequest.Query)
	}
}

func TestWebhookCountsDuplicateDeliveryOnce(t *testing.T) {
	t.Parallel()

	server, sink, webhook := newWebhookHarness(t, "acl-allow.json")
	body := readFixture(t, "webhook-updated-mapped.json")
	if response := postWebhook(t, webhook, body, webhookTestUsername, webhookTestPassword); response.Code != http.StatusAccepted {
		t.Fatalf("first delivery status = %d", response.Code)
	}
	if response := postWebhook(t, webhook, body, webhookTestUsername, webhookTestPassword); response.Code != http.StatusNoContent {
		t.Fatalf("duplicate delivery status = %d, want 204", response.Code)
	}
	proposals, _ := sink.snapshot()
	if len(proposals) != 1 {
		t.Fatalf("proposals = %d, want 1", len(proposals))
	}
	if stats := webhook.Stats(); stats.Accepted != 1 || stats.Duplicates != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	if count := server.CountPath("/acme/_apis/wit/workitems/42"); count != 1 {
		t.Fatalf("work item fetches = %d, want the duplicate delivery to stop before the API", count)
	}
}

func TestWebhookFiltersUnrelatedFieldUpdate(t *testing.T) {
	t.Parallel()

	server, sink, webhook := newWebhookHarness(t, "acl-allow.json")
	response := postWebhook(t, webhook, readFixture(t, "webhook-updated-unrelated.json"), webhookTestUsername, webhookTestPassword)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	proposals, rejections := sink.snapshot()
	if len(proposals) != 0 || len(rejections) != 0 {
		t.Fatalf("proposals = %d, rejections = %d", len(proposals), len(rejections))
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("filtered update made %d forge requests", len(requests))
	}
	if stats := webhook.Stats(); stats.Filtered != 1 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestWebhookRejectsInvalidCredentialsBeforeParsing(t *testing.T) {
	t.Parallel()

	server, sink, webhook := newWebhookHarness(t, "acl-allow.json")
	response := postWebhook(t, webhook, []byte("{not valid json"), webhookTestUsername, "wrong")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if response.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("401 response did not challenge Basic authentication")
	}
	proposals, rejections := sink.snapshot()
	if len(proposals) != 0 || len(rejections) != 0 {
		t.Fatal("an unauthenticated delivery reached the sink")
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("unauthenticated delivery made %d forge requests", len(requests))
	}
	if stats := webhook.Stats(); stats.Unauthorized != 1 {
		t.Fatalf("stats = %#v, want one counted authentication rejection", stats)
	}
}

func TestWebhookAuditsActorWithoutRepositoryWrite(t *testing.T) {
	t.Parallel()

	_, sink, webhook := newWebhookHarness(t, "acl-read-only.json")
	response := postWebhook(t, webhook, readFixture(t, "webhook-updated-mapped.json"), webhookTestUsername, webhookTestPassword)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	proposals, rejections := sink.snapshot()
	if len(proposals) != 0 {
		t.Fatalf("proposals = %d, want none", len(proposals))
	}
	if len(rejections) != 1 {
		t.Fatalf("rejections = %d, want 1", len(rejections))
	}
	rejection := rejections[0]
	if rejection.Reason != reasonActorUnauthorized {
		t.Fatalf("reason = %q", rejection.Reason)
	}
	if rejection.Actor.Descriptor != "aad.ada-lovelace" || rejection.WorkItemID != "42" || rejection.Revision != "6" {
		t.Fatalf("rejection = %#v", rejection)
	}
	if stats := webhook.Stats(); stats.Rejected != 1 || stats.Accepted != 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestWebhookBoundsBodyBeforeParsing(t *testing.T) {
	t.Parallel()

	server, sink, webhook := newWebhookHarness(t, "acl-allow.json", func(cfg *WebhookConfig) {
		cfg.MaxBodyBytes = 64
	})
	response := postWebhook(t, webhook, readFixture(t, "webhook-updated-mapped.json"), webhookTestUsername, webhookTestPassword)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.Code)
	}
	proposals, rejections := sink.snapshot()
	if len(proposals) != 0 || len(rejections) != 0 {
		t.Fatal("an oversized delivery reached the sink")
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("oversized delivery made %d forge requests", len(requests))
	}
}

func TestWebhookRejectsOutboundCredential(t *testing.T) {
	t.Parallel()

	server, sink, webhook := newWebhookHarness(t, "acl-allow.json")
	response := postWebhook(t, webhook, readFixture(t, "webhook-updated-mapped.json"), "esf", "pat")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for the outbound forge credential", response.Code)
	}
	proposals, rejections := sink.snapshot()
	if len(proposals) != 0 || len(rejections) != 0 {
		t.Fatalf("proposals = %d, rejections = %d, want none", len(proposals), len(rejections))
	}
	if requests := server.Requests(); len(requests) != 0 {
		t.Fatalf("an outbound credential made %d forge requests", len(requests))
	}
}

func TestNewWebhookRequiresDedicatedCredential(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, "https://dev.azure.com/acme", RetryPolicy{MaxAttempts: 1})
	resolver := forge.RepositoryResolverFunc(func(forge.WorkItemFields) (string, error) {
		return "https://dev.azure.com/acme/payments/_git/payments", nil
	})
	cases := []struct {
		name string
		cfg  WebhookConfig
		want string
	}{
		{name: "missing username", cfg: WebhookConfig{Secret: webhookTestSecret(t)}, want: "username"},
		{name: "missing secret", cfg: WebhookConfig{Username: webhookTestUsername}, want: "secret"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			cfg.Sink = &captureSink{}
			cfg.Repositories = resolver
			if _, err := NewWebhook(client, cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("NewWebhook() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}
