package azuredevops

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/forge"
)

const testPublicationTime = "2026-11-07T10:05:00Z"

func runTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func publicationFixture(t *testing.T) (workDir, remoteDir, baseline string, patch []byte) {
	t.Helper()
	root := t.TempDir()
	workDir = filepath.Join(root, "work")
	remoteDir = filepath.Join(root, "remote.git")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, workDir, "init", "--initial-branch=main")
	runTestGit(t, workDir, "config", "user.email", "test@example.test")
	runTestGit(t, workDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workDir, "greeting.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, workDir, "add", "greeting.txt")
	runTestGit(t, workDir, "commit", "-m", "baseline")
	baseline = runTestGit(t, workDir, "rev-parse", "HEAD")
	runTestGit(t, root, "init", "--bare", remoteDir)
	runTestGit(t, workDir, "remote", "add", "origin", remoteDir)
	runTestGit(t, workDir, "push", "origin", "main")
	if err := os.WriteFile(filepath.Join(workDir, "greeting.txt"), []byte("hello, world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, workDir, "add", "greeting.txt")
	patch = []byte(runTestGit(t, workDir, "diff", "--cached", "--no-color", "--binary") + "\n")
	runTestGit(t, workDir, "reset", "--hard", "HEAD")
	return workDir, remoteDir, baseline, patch
}

func TestBuildPublicationCommitIsDeterministicAndTreeChecked(t *testing.T) {
	t.Parallel()
	workDir, _, baseline, patch := publicationFixture(t)
	commitTime, err := time.Parse(time.RFC3339, testPublicationTime)
	if err != nil {
		t.Fatal(err)
	}
	opts := BuildPublicationOptions{
		RepositoryURL: workDir,
		BaselineSHA:   baseline,
		Patch:         patch,
		Branch:        "esf/change-a1",
		ChangeID:      "change",
		Attempt:       1,
		AuthorName:    "ESF Factory",
		AuthorEmail:   "esf@localhost",
		CommitTime:    commitTime,
	}
	first, err := BuildPublicationCommit(context.Background(), opts)
	if err != nil {
		t.Fatalf("BuildPublicationCommit: %v", err)
	}
	defer first.Close()
	second, err := BuildPublicationCommit(context.Background(), opts)
	if err != nil {
		t.Fatalf("second BuildPublicationCommit: %v", err)
	}
	defer second.Close()
	if first.Commit != second.Commit {
		t.Fatalf("commit is not deterministic: %q != %q", first.Commit, second.Commit)
	}
	if first.Tree == "" || first.Tree != second.Tree {
		t.Fatalf("tree = %q / %q", first.Tree, second.Tree)
	}
	if len(first.ChangedPaths) != 1 || first.ChangedPaths[0] != "greeting.txt" {
		t.Fatalf("changed paths = %v", first.ChangedPaths)
	}
	if !validObjectID(first.Commit) || !validObjectID(first.Tree) {
		t.Fatalf("commit/tree = %q/%q", first.Commit, first.Tree)
	}
}

func TestBuildPublicationCommitRejectsEmptyPatch(t *testing.T) {
	t.Parallel()
	workDir, _, baseline, _ := publicationFixture(t)
	_, err := BuildPublicationCommit(context.Background(), BuildPublicationOptions{
		RepositoryURL: workDir, BaselineSHA: baseline, Branch: "esf/change-a1",
		AuthorName: "ESF Factory", AuthorEmail: "esf@localhost", CommitTime: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("an empty patch produced a commit")
	}
}

func TestPublicationBranchNameIsNamespacedAndAttemptKeyed(t *testing.T) {
	t.Parallel()
	first := PublicationBranchName("workitem-change/42", 1)
	second := PublicationBranchName("workitem-change/42", 2)
	if !strings.HasPrefix(first, "esf/") || first == second {
		t.Fatalf("branch names = %q, %q", first, second)
	}
	if strings.Contains(first, "..") || strings.Contains(first, "/42/") {
		t.Fatalf("branch name is not safe: %q", first)
	}
}

func TestPublishBranchRefusesForcePushWithOpenPullRequest(t *testing.T) {
	t.Parallel()
	workDir, remoteDir, baseline, patch := publicationFixture(t)
	runTestGit(t, workDir, "push", "origin", "main:refs/heads/esf/change-a1")
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeFixture(w, []byte(`{"value":[{"pullRequestId":17,"status":"active","isDraft":true}]}`))
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	commitTime, _ := time.Parse(time.RFC3339, testPublicationTime)
	_, err := client.PublishBranch(context.Background(), forge.BranchPublication{
		Repository: server.URL() + "/acme/Payments/_git/payments", RemoteURL: remoteDir,
		BaselineSHA: baseline, Patch: patch, Branch: "esf/change-a1", ChangeID: "change", Attempt: 1,
		AuthorName: "ESF Factory", AuthorEmail: "esf@localhost", CommitTime: commitTime,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to force-push") {
		t.Fatalf("PublishBranch = %v, want a force-push refusal", err)
	}
	if got := runTestGit(t, workDir, "ls-remote", remoteDir, "refs/heads/esf/change-a1"); !strings.Contains(got, baseline) {
		t.Fatalf("remote branch changed: %s", got)
	}
}

func TestPublishBranchPushesDeterministicCommit(t *testing.T) {
	t.Parallel()
	workDir, remoteDir, baseline, patch := publicationFixture(t)
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeFixture(w, []byte(`{"value":[]}`))
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	commitTime, _ := time.Parse(time.RFC3339, testPublicationTime)
	result, err := client.PublishBranch(context.Background(), forge.BranchPublication{
		Repository: server.URL() + "/acme/Payments/_git/payments", RemoteURL: remoteDir,
		BaselineSHA: baseline, Patch: patch, Branch: "esf/change-a1", ChangeID: "change", Attempt: 1,
		AuthorName: "ESF Factory", AuthorEmail: "esf@localhost", CommitTime: commitTime,
	})
	if err != nil {
		t.Fatalf("PublishBranch: %v", err)
	}
	if result.Commit == "" || result.Tree == "" || result.AlreadyPresent {
		t.Fatalf("result = %#v", result)
	}
	if got := runTestGit(t, workDir, "ls-remote", remoteDir, "refs/heads/esf/change-a1"); !strings.Contains(got, result.Commit) {
		t.Fatalf("remote branch = %q, want %q", got, result.Commit)
	}
}

// TestBuildBranchSuppliesCredentialToCloneOnly proves the host-side baseline
// clone receives the connector's authenticated environment while every later
// local git step keeps the sanitized environment.
func TestBuildBranchSuppliesCredentialToCloneOnly(t *testing.T) {
	_, remoteDir, baseline, patch := publicationFixture(t)

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("resolve git: %v", err)
	}
	if !filepath.IsAbs(realGit) {
		if realGit, err = filepath.Abs(realGit); err != nil {
			t.Fatalf("absolute git path: %v", err)
		}
	}
	shimDir := t.TempDir()
	logPath := filepath.Join(shimDir, "git-calls.log")
	shim := "#!/bin/sh\n" +
		"printf '%s\\t%s\\t%s\\t%s\\n' \"$*\" \"${GIT_ASKPASS-}\" \"${ESF_GIT_PASSWORD:+set}\" \"$([ -x \"${GIT_ASKPASS-}\" ] && echo 1 || echo 0)\" >> '" + logPath + "'\n" +
		"exec '" + realGit + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shimDir, "git"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write git shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	client := newTestClient(t, "https://dev.azure.com/acme", RetryPolicy{MaxAttempts: 1})
	commitTime, err := time.Parse(time.RFC3339, testPublicationTime)
	if err != nil {
		t.Fatal(err)
	}
	build, err := client.BuildBranch(context.Background(), forge.BranchPublication{
		Repository:  "https://dev.azure.com/acme/Payments/_git/payments",
		RemoteURL:   remoteDir,
		BaselineSHA: baseline,
		Patch:       patch,
		Branch:      "esf/change-a1",
		ChangeID:    "change",
		Attempt:     1,
		AuthorName:  "ESF Factory",
		AuthorEmail: "esf@localhost",
		CommitTime:  commitTime,
	})
	if err != nil {
		t.Fatalf("BuildBranch: %v", err)
	}
	client.DiscardBranch(build)

	recorded, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read git shim log: %v", err)
	}
	var sawClone, sawLocal bool
	for _, line := range strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			t.Fatalf("unexpected git shim record %q", line)
		}
		args, askpass, password, askpassExecutable := fields[0], fields[1], fields[2], fields[3]
		if !strings.HasPrefix(args, "clone ") {
			sawLocal = true
			if askpass != "/bin/false" || password != "" {
				t.Fatalf("local git step %q gained the connector credential: GIT_ASKPASS=%q ESF_GIT_PASSWORD=%q", args, askpass, password)
			}
			continue
		}
		sawClone = true
		if password != "set" || askpassExecutable != "1" {
			t.Fatalf("clone lacks a live connector askpass: GIT_ASKPASS=%q ESF_GIT_PASSWORD=%q askpassExecutable=%q", askpass, password, askpassExecutable)
		}
	}
	if !sawClone || !sawLocal {
		t.Fatalf("shim saw clone=%v local=%v\n%s", sawClone, sawLocal, recorded)
	}
}

func TestPublishHonoursRetryAfter(t *testing.T) {
	t.Parallel()
	var attempts int
	var slept time.Duration
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "throttled", http.StatusTooManyRequests)
			return
		}
		writeFixture(w, []byte(`{"value":[]}`))
	})
	credential := testCredential(t)
	client, err := NewClient(Config{
		Name: "azdo", BaseURL: server.URL() + "/acme", Credential: credential,
		Retry: RetryPolicy{MaxAttempts: 2, Sleep: func(_ context.Context, delay time.Duration) error {
			slept = delay
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Publish(context.Background(), forge.PublicationPlan{
		Repository: server.URL() + "/acme/Payments/_git/payments", BaseBranch: "main",
		Branch: "esf/change-a1", Title: "title", Body: "body", Draft: true,
	}); err == nil {
		// The second response is a list, so the create call fails distinctly.
		t.Fatal("Publish unexpectedly succeeded")
	}
	if attempts != 2 || slept != 2*time.Second {
		t.Fatalf("attempts/slept = %d/%s, want 2/2s", attempts, slept)
	}
}

func TestReadAcceptanceRecordsVotesAndMergeTree(t *testing.T) {
	t.Parallel()
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/acme/Payments/_apis/git/repositories/payments/pullRequests/17":
			writeFixture(w, []byte(`{
				"pullRequestId":17,"status":"completed","isDraft":false,
				"lastMergeCommit":{"commitId":"merge-1"},
				"lastMergeSourceCommit":{"commitId":"source-1"},
				"closedBy":{"displayName":"Ada Lovelace","uniqueName":"ada@example.test","descriptor":"aad.ada"},
				"autoCompleteSetBy":{"displayName":"Grace Hopper","uniqueName":"grace@example.test"},
				"reviewers":[
					{"id":"1","displayName":"Ada","uniqueName":"ada@example.test","vote":10},
					{"id":"2","displayName":"Linus","uniqueName":"linus@example.test","vote":-10}
				]
			}`))
		case r.URL.Path == "/acme/Payments/_apis/git/repositories/payments/commits/merge-1":
			writeFixture(w, []byte(`{"commitId":"merge-1","treeId":"tree-1"}`))
		default:
			http.NotFound(w, r)
		}
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	result, err := client.ReadAcceptance(context.Background(), forge.AcceptanceRequest{
		Repository: server.URL() + "/acme/Payments/_git/payments", PullRequestID: "17",
	})
	if err != nil {
		t.Fatalf("ReadAcceptance: %v", err)
	}
	if result.State != forge.AcceptanceMerged || result.MergeCommit != "merge-1" || result.MergeTree != "tree-1" {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Votes) != 2 || result.Votes[0].Vote != 10 || result.Votes[1].Vote != -10 {
		t.Fatalf("votes = %#v", result.Votes)
	}
	if result.AutoCompleteSetBy != "grace@example.test" || result.ClosedBy != "aad.ada" {
		t.Fatalf("actors = %q / %q", result.AutoCompleteSetBy, result.ClosedBy)
	}
}

func TestPublishAttachesBundleToExistingPullRequest(t *testing.T) {
	t.Parallel()
	var attached bool
	server := newForgeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pullRequests") && r.Method == http.MethodGet:
			writeFixture(w, []byte(`{"value":[{"pullRequestId":17,"status":"active","isDraft":true}]}`))
		case strings.HasSuffix(r.URL.Path, "/pullRequests/17/attachments/evidence-bundle.json"):
			attached = true
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	})
	client := newTestClient(t, server.URL()+"/acme", RetryPolicy{MaxAttempts: 1})
	result, err := client.Publish(context.Background(), forge.PublicationPlan{
		Repository: server.URL() + "/acme/Payments/_git/payments", BaseBranch: "main",
		Branch: "esf/change-a1", Title: "title", Body: "body", Draft: true,
		Attachment: &forge.Attachment{Name: forge.EvidenceBundleAttachmentName, MediaType: "application/json", Data: []byte(`{"bundle":true}`)},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.PullRequest.ID != "17" || !attached {
		t.Fatalf("result = %#v, attached=%v", result, attached)
	}
}

func TestReadAcceptanceMapsReviewersJSON(t *testing.T) {
	t.Parallel()
	var pr azurePullRequest
	if err := json.Unmarshal([]byte(`{"reviewers":[{"vote":5}]}`), &pr); err != nil {
		t.Fatal(err)
	}
	if len(pr.Reviewers) != 1 || pr.Reviewers[0].Vote != 5 {
		t.Fatalf("reviewers = %#v", pr.Reviewers)
	}
}
