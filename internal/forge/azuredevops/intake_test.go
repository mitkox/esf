package azuredevops

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func writeCredentialFile(t *testing.T, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	return path
}

func testCredential(t *testing.T) forge.Credential {
	t.Helper()
	credential, err := forge.ReadCredential(writeCredentialFile(t, "esf:pat"))
	if err != nil {
		t.Fatalf("read credential: %v", err)
	}
	return credential
}

func newTestClient(t *testing.T, baseURL string, retry RetryPolicy) *Client {
	t.Helper()
	client, err := NewClient(Config{
		Name:       "azdo",
		BaseURL:    baseURL,
		Credential: testCredential(t),
		Retry:      retry,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

type forgeRequest struct {
	Method string
	Path   string
	Query  url.Values
}

type forgeServer struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	records []forgeRequest
	handler http.HandlerFunc
}

func newForgeServer(t *testing.T, handler http.HandlerFunc) *forgeServer {
	t.Helper()
	server := &forgeServer{t: t, handler: handler}
	server.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.records = append(server.records, forgeRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query()})
		server.mu.Unlock()
		username, password, ok := r.BasicAuth()
		if !ok || username != "esf" || password != "pat" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		server.handler(w, r)
	}))
	t.Cleanup(server.server.Close)
	return server
}

func (s *forgeServer) URL() string { return s.server.URL }

func (s *forgeServer) Requests() []forgeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]forgeRequest(nil), s.records...)
}

func (s *forgeServer) CountPath(path string) int {
	count := 0
	for _, record := range s.Requests() {
		if record.Path == path {
			count++
		}
	}
	return count
}

func writeFixture(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func TestSweepReturnsNextCursorAndKeepsNewestRevision(t *testing.T) {
	t.Parallel()

	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/_apis/wit/reporting/workitemrevisions":
			switch r.URL.Query().Get("continuationToken") {
			case "watermark-0":
				writeFixture(w, readFixture(t, "revisions-page1.json"))
			case "watermark-1":
				writeFixture(w, readFixture(t, "revisions-page2.json"))
			default:
				t.Errorf("unexpected continuation token %q", r.URL.Query().Get("continuationToken"))
				http.Error(w, "unexpected token", http.StatusBadRequest)
			}
		case "/acme/_apis/wit/workitems/42":
			writeFixture(w, readFixture(t, "workitem-42.json"))
		case "/acme/_apis/wit/workitems/43":
			writeFixture(w, readFixture(t, "workitem-43.json"))
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})

	watermark, err := NewFileWatermark(filepath.Join(t.TempDir(), "watermark"))
	if err != nil {
		t.Fatalf("NewFileWatermark: %v", err)
	}
	if err := watermark.Save(context.Background(), "watermark-0"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sweeper, err := NewSweeper(newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1}), watermark, SweeperOptions{PageSize: 2, MaxPages: 5})
	if err != nil {
		t.Fatalf("NewSweeper: %v", err)
	}

	result, err := sweeper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if result.NextCursor != "watermark-2" {
		t.Fatalf("next cursor = %q, want watermark-2", result.NextCursor)
	}
	candidates := result.Items
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(candidates))
	}
	if candidates[0].WorkItem.ID != "43" || candidates[0].WorkItem.Revision != "2" {
		t.Fatalf("first candidate = %s rev %s, want work item 43 rev 2", candidates[0].WorkItem.ID, candidates[0].WorkItem.Revision)
	}
	if candidates[1].WorkItem.ID != "42" || candidates[1].WorkItem.Revision != "6" {
		t.Fatalf("second candidate = %s rev %s, want work item 42 rev 6", candidates[1].WorkItem.ID, candidates[1].WorkItem.Revision)
	}
	if candidates[1].Source != forge.SourceSweep {
		t.Fatalf("source = %q", candidates[1].Source)
	}
	if candidates[1].Connector != "azdo" {
		t.Fatalf("connector = %q", candidates[1].Connector)
	}
	wantHash := forge.DigestWorkItem(readFixture(t, "workitem-42.json"))
	if candidates[1].WorkItemHash != wantHash {
		t.Fatalf("work item hash = %q, want %q", candidates[1].WorkItemHash, wantHash)
	}
	stored, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-0" {
		t.Fatalf("watermark after Sweep = %q, want the unadvanced watermark-0", stored)
	}
	if err := sweeper.CommitSweep(context.Background(), result.NextCursor); err != nil {
		t.Fatalf("CommitSweep: %v", err)
	}
	stored, err = watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-2" {
		t.Fatalf("watermark after CommitSweep = %q, want watermark-2", stored)
	}

	requests := server.Requests()
	if len(requests) != 4 {
		t.Fatalf("requests = %d, want 4 (two revision pages and two work items)", len(requests))
	}
	firstPage := requests[0]
	if firstPage.Path != "/acme/_apis/wit/reporting/workitemrevisions" || firstPage.Query.Get("continuationToken") != "watermark-0" || firstPage.Query.Get("$maxPageSize") != "2" {
		t.Fatalf("first revision request = %#v", firstPage)
	}
	secondPage := requests[1]
	if secondPage.Query.Get("continuationToken") != "watermark-1" {
		t.Fatalf("second revision request = %#v", secondPage)
	}
}

// TestSweepDoesNotSaveWatermarkBeforeDelivery proves a sweep returns its
// candidates without recording the continuation token. Only the caller that
// delivered every candidate may commit the token.
func TestSweepDoesNotSaveWatermarkBeforeDelivery(t *testing.T) {
	t.Parallel()

	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/_apis/wit/reporting/workitemrevisions":
			switch r.URL.Query().Get("continuationToken") {
			case "watermark-0":
				writeFixture(w, readFixture(t, "revisions-page1.json"))
			case "watermark-1":
				writeFixture(w, readFixture(t, "revisions-page2.json"))
			default:
				http.Error(w, "unexpected token", http.StatusBadRequest)
			}
		case "/acme/_apis/wit/workitems/42":
			writeFixture(w, readFixture(t, "workitem-42.json"))
		case "/acme/_apis/wit/workitems/43":
			writeFixture(w, readFixture(t, "workitem-43.json"))
		default:
			http.NotFound(w, r)
		}
	})

	watermark, err := NewFileWatermark(filepath.Join(t.TempDir(), "watermark"))
	if err != nil {
		t.Fatalf("NewFileWatermark: %v", err)
	}
	if err := watermark.Save(context.Background(), "watermark-0"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sweeper, err := NewSweeper(newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1}), watermark, SweeperOptions{PageSize: 2, MaxPages: 5})
	if err != nil {
		t.Fatalf("NewSweeper: %v", err)
	}

	result, err := sweeper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if result.NextCursor != "watermark-2" {
		t.Fatalf("next cursor = %q, want watermark-2", result.NextCursor)
	}
	if len(result.Items) != 2 {
		t.Fatalf("candidates = %d, want 2", len(result.Items))
	}
	stored, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-0" {
		t.Fatalf("watermark after Sweep = %q, want the unadvanced watermark-0", stored)
	}
	if err := sweeper.CommitSweep(context.Background(), result.NextCursor); err != nil {
		t.Fatalf("CommitSweep: %v", err)
	}
	stored, err = watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-2" {
		t.Fatalf("watermark after CommitSweep = %q, want watermark-2", stored)
	}
}

func TestSweepReturnsResumeTokenAtPageBound(t *testing.T) {
	t.Parallel()

	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/_apis/wit/reporting/workitemrevisions":
			writeFixture(w, readFixture(t, "revisions-page1.json"))
		case "/acme/_apis/wit/workitems/42":
			writeFixture(w, readFixture(t, "workitem-42.json"))
		case "/acme/_apis/wit/workitems/43":
			writeFixture(w, readFixture(t, "workitem-43.json"))
		default:
			http.NotFound(w, r)
		}
	})
	watermark, err := NewFileWatermark(filepath.Join(t.TempDir(), "watermark"))
	if err != nil {
		t.Fatalf("NewFileWatermark: %v", err)
	}
	sweeper, err := NewSweeper(newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1}), watermark, SweeperOptions{PageSize: 2, MaxPages: 1})
	if err != nil {
		t.Fatalf("NewSweeper: %v", err)
	}
	result, err := sweeper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("candidates = %d, want 2", len(result.Items))
	}
	if count := server.CountPath("/acme/_apis/wit/reporting/workitemrevisions"); count != 1 {
		t.Fatalf("revision requests = %d, want 1 bounded page", count)
	}
	if result.NextCursor != "watermark-1" {
		t.Fatalf("next cursor = %q, want the next page token", result.NextCursor)
	}
	stored, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "" {
		t.Fatalf("watermark after Sweep = %q, want it unadvanced", stored)
	}
	if err := sweeper.CommitSweep(context.Background(), result.NextCursor); err != nil {
		t.Fatalf("CommitSweep: %v", err)
	}
	stored, err = watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-1" {
		t.Fatalf("watermark = %q, want the next page token", stored)
	}
}

func TestSweepBacksOffOn429WithoutLosingWatermark(t *testing.T) {
	t.Parallel()

	var throttled atomic.Bool
	throttled.Store(true)
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if throttled.Load() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "throttled", http.StatusTooManyRequests)
			return
		}
		switch r.URL.Path {
		case "/acme/_apis/wit/reporting/workitemrevisions":
			writeFixture(w, readFixture(t, "revisions-after-throttle.json"))
		case "/acme/_apis/wit/workitems/43":
			writeFixture(w, readFixture(t, "workitem-43.json"))
		default:
			http.NotFound(w, r)
		}
	})

	var mu sync.Mutex
	var delays []time.Duration
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
		Sleep: func(_ context.Context, delay time.Duration) error {
			mu.Lock()
			delays = append(delays, delay)
			mu.Unlock()
			return nil
		},
	})
	watermark, err := NewFileWatermark(filepath.Join(t.TempDir(), "watermark"))
	if err != nil {
		t.Fatalf("NewFileWatermark: %v", err)
	}
	if err := watermark.Save(context.Background(), "watermark-0"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sweeper, err := NewSweeper(client, watermark, SweeperOptions{PageSize: 2, MaxPages: 5})
	if err != nil {
		t.Fatalf("NewSweeper: %v", err)
	}

	if _, err := sweeper.Sweep(context.Background()); err == nil || !IsThrottled(err) {
		t.Fatalf("Sweep() = %v, want a throttled error", err)
	}
	mu.Lock()
	recordedDelays := append([]time.Duration(nil), delays...)
	mu.Unlock()
	if len(recordedDelays) != 2 {
		t.Fatalf("backoff delays = %v, want two retries", recordedDelays)
	}
	for _, delay := range recordedDelays {
		if delay < time.Second {
			t.Fatalf("backoff delay = %v, want the Retry-After duration honored", delay)
		}
	}
	stored, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-0" {
		t.Fatalf("watermark after throttling = %q, want watermark-0", stored)
	}
	if count := server.CountPath("/acme/_apis/wit/reporting/workitemrevisions"); count != 3 {
		t.Fatalf("revision attempts = %d, want 3", count)
	}

	throttled.Store(false)
	result, err := sweeper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("resumed Sweep: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].WorkItem.ID != "43" {
		t.Fatalf("resumed candidates = %#v", result.Items)
	}
	if result.NextCursor != "watermark-after-429" {
		t.Fatalf("resumed next cursor = %q, want watermark-after-429", result.NextCursor)
	}
	stored, err = watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-0" {
		t.Fatalf("watermark after resume = %q, want it unadvanced until the cursor is committed", stored)
	}
	if err := sweeper.CommitSweep(context.Background(), result.NextCursor); err != nil {
		t.Fatalf("CommitSweep: %v", err)
	}
	stored, err = watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if stored != "watermark-after-429" {
		t.Fatalf("watermark after resume = %q, want watermark-after-429", stored)
	}
	requests := server.Requests()
	var resumedToken string
	for _, request := range requests {
		if request.Path == "/acme/_apis/wit/reporting/workitemrevisions" && request.Query.Get("continuationToken") != "" {
			resumedToken = request.Query.Get("continuationToken")
		}
	}
	if resumedToken != "watermark-0" {
		t.Fatalf("resumed sweep used token %q, want watermark-0", resumedToken)
	}
}

func TestCloneCredentialBuildsEgressInjectionRules(t *testing.T) {
	t.Parallel()

	secretFile := writeCredentialFile(t, "esf:pat")
	credential, err := forge.ReadCredential(secretFile)
	if err != nil {
		t.Fatalf("ReadCredential: %v", err)
	}
	client, err := NewClient(Config{
		Name:       "azdo",
		BaseURL:    "https://dev.azure.com/acme",
		Credential: credential,
		Clone: CloneCredential{
			SecretFile: secretFile,
			Host:       "dev.azure.com",
			Paths: []string{
				"/acme/Payments/_git/payments/info/refs",
				"/acme/Payments/_git/payments/git-upload-pack",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	rules, err := client.CloneEgressRules()
	if err != nil {
		t.Fatalf("CloneEgressRules: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(rules))
	}
	wantSecret := base64.StdEncoding.EncodeToString([]byte("esf:pat"))
	for i, rule := range rules {
		if !rule.Action.Allow {
			t.Fatalf("rule %d does not allow", i)
		}
		if rule.Match.Scheme != "https" || rule.Match.Host != "dev.azure.com" || rule.Match.SNI != "dev.azure.com" {
			t.Fatalf("rule %d match = %#v", i, rule.Match)
		}
		if len(rule.Match.Method) != 2 || rule.Match.Method[0] != "GET" || rule.Match.Method[1] != "POST" {
			t.Fatalf("rule %d methods = %v", i, rule.Match.Method)
		}
		if len(rule.Action.Inject) != 1 {
			t.Fatalf("rule %d injections = %#v", i, rule.Action.Inject)
		}
		injection := rule.Action.Inject[0]
		if injection.Header != "Authorization" || injection.Format != "Basic ${SECRET}" {
			t.Fatalf("rule %d injection = %#v", i, injection)
		}
		if injection.Secret != wantSecret {
			t.Fatalf("rule %d injected secret is not the expected Basic value", i)
		}
		if strings.Contains(injection.Secret, "esf:pat") {
			t.Fatalf("rule %d injected the raw credential", i)
		}
	}
	if strings.Contains(fmt.Sprintf("%v", client), "esf:pat") {
		t.Fatal("the connector string form leaked the clone credential")
	}
	if rules[0].Match.Path != "/acme/Payments/_git/payments/info/refs" || rules[1].Match.Path != "/acme/Payments/_git/payments/git-upload-pack" {
		t.Fatalf("rule paths = %q, %q", rules[0].Match.Path, rules[1].Match.Path)
	}
}

func TestCloneCredentialRejectsUnsafeDeclarations(t *testing.T) {
	t.Parallel()

	goodSecret := writeCredentialFile(t, "esf:pat")
	unsafeSecret := filepath.Join(t.TempDir(), "unsafe")
	if err := os.WriteFile(unsafeSecret, []byte("esf:pat"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		credential CloneCredential
		want       string
	}{
		{
			name:       "relative secret file",
			credential: CloneCredential{SecretFile: "secret", Host: "dev.azure.com", Paths: []string{"/acme/Payments/_git/payments"}},
			want:       "absolute",
		},
		{
			name:       "unsafe secret file",
			credential: CloneCredential{SecretFile: unsafeSecret, Host: "dev.azure.com", Paths: []string{"/acme/Payments/_git/payments"}},
			want:       "permissions",
		},
		{
			name:       "ip host",
			credential: CloneCredential{SecretFile: goodSecret, Host: "192.0.2.1", Paths: []string{"/acme/Payments/_git/payments"}},
			want:       "DNS hostname",
		},
		{
			name:       "no paths",
			credential: CloneCredential{SecretFile: goodSecret, Host: "dev.azure.com"},
			want:       "endpoint path",
		},
		{
			name:       "relative path",
			credential: CloneCredential{SecretFile: goodSecret, Host: "dev.azure.com", Paths: []string{"acme/Payments/_git/payments"}},
			want:       "absolute path",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tc.credential.EgressRules(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("EgressRules() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestFetchWorkItemUsesExactResponseBytes(t *testing.T) {
	t.Parallel()

	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/_apis/wit/workitems/42" {
			http.NotFound(w, r)
			return
		}
		writeFixture(w, readFixture(t, "workitem-42.json"))
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	snapshot, err := client.FetchWorkItem(context.Background(), 42)
	if err != nil {
		t.Fatalf("FetchWorkItem: %v", err)
	}
	if snapshot.WorkItemHash != forge.DigestWorkItem(readFixture(t, "workitem-42.json")) {
		t.Fatalf("hash = %q", snapshot.WorkItemHash)
	}
	if snapshot.Actor.Descriptor != "aad.ada-lovelace" || snapshot.Actor.DisplayName != "Ada Lovelace" {
		t.Fatalf("actor = %#v", snapshot.Actor)
	}
	if snapshot.WorkItem.Title != "Payment retry loop spins forever" {
		t.Fatalf("title = %q", snapshot.WorkItem.Title)
	}
}

func TestRepositoryWriteRequiresContribute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		acl     string
		allowed bool
	}{
		{name: "contribute", acl: "acl-allow.json", allowed: true},
		{name: "read only", acl: "acl-read-only.json", allowed: false},
		{name: "inherited from project", acl: "acl-empty.json", allowed: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/acme/Payments/_apis/git/repositories/payments":
					writeFixture(w, readFixture(t, "repository.json"))
				case "/acme/_apis/accesscontrollists/" + gitRepositoriesNamespace:
					token := r.URL.Query().Get("token")
					if token == "repoV2/project-payments" {
						writeFixture(w, readFixture(t, "acl-allow.json"))
						return
					}
					writeFixture(w, readFixture(t, tc.acl))
				default:
					http.NotFound(w, r)
				}
			})
			client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
			allowed, err := client.RepositoryWrite(context.Background(), server.URL()+"/acme/Payments/_git/payments", "aad.ada-lovelace")
			if err != nil {
				t.Fatalf("RepositoryWrite: %v", err)
			}
			if allowed != tc.allowed {
				t.Fatalf("RepositoryWrite() = %v, want %v", allowed, tc.allowed)
			}
		})
	}
}

func TestRepositoryWriteRejectsForeignRepository(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, "https://dev.azure.com/acme", RetryPolicy{MaxAttempts: 1})
	if _, err := client.RepositoryWrite(context.Background(), "https://attacker.example/acme/Payments/_git/payments", "aad.ada-lovelace"); err == nil {
		t.Fatal("RepositoryWrite accepted a repository outside the connector base URL")
	}
	if _, err := client.RepositoryWrite(context.Background(), "https://dev.azure.com/acme/Payments/_git/payments", ""); err == nil {
		t.Fatal("RepositoryWrite accepted an empty descriptor")
	}
}

func TestFileWatermarkRoundTrip(t *testing.T) {
	t.Parallel()

	watermark, err := NewFileWatermark(filepath.Join(t.TempDir(), "watermark"))
	if err != nil {
		t.Fatalf("NewFileWatermark: %v", err)
	}
	value, err := watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if value != "" {
		t.Fatalf("empty watermark = %q", value)
	}
	if err := watermark.Save(context.Background(), "813;350;1"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	value, err = watermark.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if value != "813;350;1" {
		t.Fatalf("watermark = %q", value)
	}
	if _, err := NewFileWatermark("relative"); err == nil {
		t.Fatal("NewFileWatermark accepted a relative path")
	}
}

func TestIsThrottled(t *testing.T) {
	t.Parallel()

	if !IsThrottled(&APIError{StatusCode: http.StatusTooManyRequests}) {
		t.Fatal("IsThrottled did not recognize 429")
	}
	if IsThrottled(errors.New("network failure")) {
		t.Fatal("IsThrottled recognized a generic error")
	}
}
