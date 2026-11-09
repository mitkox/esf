package forge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testBundleDigest() string {
	return "sha256:" + strings.Repeat("a", 64)
}

func TestCapabilitiesValidate(t *testing.T) {
	t.Parallel()

	valid := Capabilities{
		DraftPullRequests: true,
		MaxBodyBytes:      4096,
		StatusKind:        StatusKindPullRequestStatus,
		Attachments:       true,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid capabilities: %v", err)
	}

	cases := []struct {
		name string
		caps Capabilities
		want string
	}{
		{"missing body limit", Capabilities{StatusKind: StatusKindCommitStatus}, "maximum body bytes"},
		{"missing status kind", Capabilities{MaxBodyBytes: 4096}, "status kind"},
		{"unknown status kind", Capabilities{MaxBodyBytes: 4096, StatusKind: "invented"}, "status kind"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.caps.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestPlanPublicationRespectsBodyLimit(t *testing.T) {
	t.Parallel()

	caps := Capabilities{
		DraftPullRequests: true,
		MaxBodyBytes:      160,
		StatusKind:        StatusKindPullRequestStatus,
		Attachments:       true,
	}
	bundle := []byte(`{"payload":"evidence"}`)
	plan, err := PlanPublication(caps, Publication{
		WorkItem:     WorkItemRef{ID: "42", Revision: "7", Project: "Payments"},
		Repository:   "https://dev.azure.com/acme/payments/_git/payments",
		BaseBranch:   "main",
		Branch:       "esf/work-item-42-r7",
		Title:        "Fix the payment retry loop",
		Summary:      strings.Repeat("A deliberately long summary. ", 40),
		BundleDigest: testBundleDigest(),
		Bundle:       bundle,
		Draft:        true,
	})
	if err != nil {
		t.Fatalf("PlanPublication: %v", err)
	}
	if len(plan.Body) > caps.MaxBodyBytes {
		t.Fatalf("body is %d bytes, limit is %d", len(plan.Body), caps.MaxBodyBytes)
	}
	if !strings.Contains(plan.Body, testBundleDigest()) {
		t.Fatalf("body dropped the evidence digest: %q", plan.Body)
	}
	if plan.Attachment == nil {
		t.Fatal("an attachment-capable forge must receive the bundle")
	}
	if plan.Attachment.Name != EvidenceBundleAttachmentName {
		t.Fatalf("attachment name = %q", plan.Attachment.Name)
	}
	if string(plan.Attachment.Data) != string(bundle) {
		t.Fatal("attachment does not carry the bundle bytes")
	}
	if !plan.Draft {
		t.Fatal("the requested draft was not planned as a draft")
	}
	if plan.StatusKind != caps.StatusKind {
		t.Fatalf("status kind = %q, want %q", plan.StatusKind, caps.StatusKind)
	}
}

func TestPlanPublicationSkipsUnsupportedAttachment(t *testing.T) {
	t.Parallel()

	plan, err := PlanPublication(Capabilities{
		MaxBodyBytes: 4096,
		StatusKind:   StatusKindCommitStatus,
	}, Publication{
		Title:        "Fix the payment retry loop",
		Summary:      "Short summary.",
		BundleDigest: testBundleDigest(),
		Bundle:       []byte("bundle"),
	})
	if err != nil {
		t.Fatalf("PlanPublication: %v", err)
	}
	if plan.Attachment != nil {
		t.Fatalf("a forge without attachment support received %#v", plan.Attachment)
	}
	if strings.Contains(plan.Body, "attached") {
		t.Fatalf("body claims an attachment that will not be sent: %q", plan.Body)
	}
	if len(plan.Body) > 4096 {
		t.Fatalf("body is %d bytes", len(plan.Body))
	}
}

func TestPlanPublicationRejectsImpossibleRequests(t *testing.T) {
	t.Parallel()

	base := Publication{
		Title:        "Fix the payment retry loop",
		BundleDigest: testBundleDigest(),
		Draft:        true,
	}
	cases := []struct {
		name string
		caps Capabilities
		pub  Publication
		want string
	}{
		{
			name: "draft unsupported",
			caps: Capabilities{MaxBodyBytes: 4096, StatusKind: StatusKindCommitStatus},
			pub:  base,
			want: "draft",
		},
		{
			name: "body limit smaller than the digest",
			caps: Capabilities{MaxBodyBytes: 32, StatusKind: StatusKindCommitStatus},
			pub:  Publication{Title: base.Title, BundleDigest: base.BundleDigest},
			want: "too small",
		},
		{
			name: "missing title",
			caps: Capabilities{MaxBodyBytes: 4096, StatusKind: StatusKindCommitStatus},
			pub:  Publication{BundleDigest: base.BundleDigest},
			want: "title",
		},
		{
			name: "missing digest",
			caps: Capabilities{MaxBodyBytes: 4096, StatusKind: StatusKindCommitStatus},
			pub:  Publication{Title: base.Title},
			want: "digest",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := PlanPublication(tc.caps, tc.pub); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("PlanPublication() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestFakeConnectorRecordsAndReturnsConfiguredResults(t *testing.T) {
	t.Parallel()

	caps := Capabilities{
		DraftPullRequests: true,
		MaxBodyBytes:      1024,
		StatusKind:        StatusKindCheckRun,
	}
	fake := NewFake(caps)
	if got := fake.Capabilities(); got != caps {
		t.Fatalf("Capabilities() = %#v, want %#v", got, caps)
	}

	ctx := context.Background()
	report := ReportRequest{
		WorkItem: WorkItemRef{ID: "42"},
		Status:   Status{Name: "esf/verification", State: StatusSucceeded},
		Comment:  "Evidence bundle attached.",
	}
	if err := fake.Report(ctx, report); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(fake.Reports) != 1 || fake.Reports[0] != report {
		t.Fatalf("reports = %#v", fake.Reports)
	}

	reconciliation := ReconcileResult{
		WorkItems: []WorkItem{{ID: "43", Revision: "2"}},
		Cursor:    "next",
	}
	fake.ReconcileResult = reconciliation
	gotReconciliation, err := fake.Reconcile(ctx, ReconcileRequest{Cursor: "start", Limit: 10})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(gotReconciliation.WorkItems) != 1 || gotReconciliation.WorkItems[0].ID != "43" || gotReconciliation.Cursor != "next" {
		t.Fatalf("Reconcile() = %#v", gotReconciliation)
	}
	if len(fake.Reconciliations) != 1 || fake.Reconciliations[0].Cursor != "start" {
		t.Fatalf("reconciliations = %#v", fake.Reconciliations)
	}

	plan := PublicationPlan{
		Title: "Fix the retry loop",
		Body:  "Summary.\n\nEvidence bundle digest: " + testBundleDigest(),
		Draft: true,
	}
	fake.PublishResult = PublishResult{PullRequest: PullRequest{ID: "17", URL: "https://forge.example/pr/17", Draft: true}}
	gotPublish, err := fake.Publish(ctx, plan)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if gotPublish.PullRequest.ID != "17" || !gotPublish.PullRequest.Draft {
		t.Fatalf("Publish() = %#v", gotPublish)
	}
	if len(fake.Publications) != 1 || fake.Publications[0].Title != plan.Title {
		t.Fatalf("publications = %#v", fake.Publications)
	}

	fake.AcceptanceResult = AcceptanceResult{
		PullRequest: PullRequest{ID: "17"},
		State:       AcceptanceMerged,
	}
	gotAcceptance, err := fake.ReadAcceptance(ctx, AcceptanceRequest{Repository: "repo", PullRequestID: "17"})
	if err != nil {
		t.Fatalf("ReadAcceptance: %v", err)
	}
	if gotAcceptance.State != AcceptanceMerged {
		t.Fatalf("ReadAcceptance() = %#v", gotAcceptance)
	}
	if len(fake.Acceptances) != 1 || fake.Acceptances[0].PullRequestID != "17" {
		t.Fatalf("acceptances = %#v", fake.Acceptances)
	}

	fake.ReportErr = errors.New("forge unavailable")
	if err := fake.Report(ctx, report); !errors.Is(err, fake.ReportErr) {
		t.Fatalf("Report error = %v", err)
	}
}

func TestReadCredentialEnforcesPrivateFileRules(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	good := filepath.Join(dir, "token")
	if err := os.WriteFile(good, []byte("  secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	credential, err := ReadCredential(good)
	if err != nil {
		t.Fatalf("ReadCredential: %v", err)
	}
	if credential.Value() != "secret-token" {
		t.Fatalf("Value() = %q", credential.Value())
	}
	if credential.String() != "[redacted]" {
		t.Fatalf("String() = %q", credential.String())
	}

	groupReadable := filepath.Join(dir, "group-readable")
	if err := os.WriteFile(groupReadable, []byte("token"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	embeddedNewline := filepath.Join(dir, "newline")
	if err := os.WriteFile(embeddedNewline, []byte("one\ntwo"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{groupReadable, link, filepath.Join(dir, "missing"), embeddedNewline} {
		if _, err := ReadCredential(path); err == nil {
			t.Fatalf("ReadCredential(%q) accepted an unsafe credential", path)
		}
	}
}
