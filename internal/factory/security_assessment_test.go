package factory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/securityassessment"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

func securityProfileFixture(t *testing.T) SecurityAssessmentProfileConfig {
	t.Helper()
	p := securityassessment.Profile{Schema: securityassessment.ProfileSchema, ID: "tenant-http", TargetURL: "https://pending.esf.invalid",
		Runtime: securityassessment.RuntimePin{SourceRevision: strings.Repeat("1", 40), ImageDigest: "sha256:" + strings.Repeat("2", 64), CatalogDigest: strings.Repeat("3", 64), TemplateID: "tpl-test", Tools: map[string]string{"oc_http_request": "0.5.0"}},
		Limits:  securityassessment.DefaultLimits(), RequiredTools: []string{"oc_http_request"}, RequiredChecks: []string{"tenant-isolation"}}
	profile, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	qualification, err := json.Marshal(SecurityQualification{Schema: SecurityQualificationSchema, RouteQualified: true, CleanupVerified: true, Runtime: p.Runtime})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	qualificationPath := filepath.Join(dir, "qualification.json")
	if err := os.WriteFile(profilePath, profile, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(qualificationPath, qualification, 0600); err != nil {
		t.Fatal(err)
	}
	return SecurityAssessmentProfileConfig{ProfilePath: profilePath, ProfileSHA256: securityassessment.SHA256(profile), QualificationPath: qualificationPath, QualificationSHA256: securityassessment.SHA256(qualification)}
}

func TestSecurityAssessmentRequiresDeclaredProfileAndScopeGrant(t *testing.T) {
	p := securityProfileFixture(t)
	cfg := Config{SecurityAssessmentProfiles: map[string]SecurityAssessmentProfileConfig{"tenant-http": p}}
	req := RunRequest{SecurityAssessmentProfile: "tenant-http"}
	if _, err := cfg.ResolveResources(req); err == nil {
		t.Fatal("profile without explicit scope grant was admitted")
	}
	cfg.Scopes = map[string]ScopeConfig{DefaultScope: {SecurityAssessments: []string{"tenant-http"}}}
	resolved, err := cfg.ResolveResources(req)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SecurityAssessmentProfile != "tenant-http" || resolved.SecurityAssessmentConfig == nil {
		t.Fatal("declared profile was not frozen into resources")
	}
	req.SecurityAssessmentProfile = "unknown"
	if _, err := cfg.ResolveResources(req); err == nil {
		t.Fatal("caller-selected undeclared profile was admitted")
	}
	resolved, err = cfg.ResolveResources(RunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SecurityAssessmentProfile != "" {
		t.Fatal("declaring a profile silently enabled assessment")
	}
}

func TestSecurityAdvicePreservesConfirmedFailureWithUnobservedAssertion(t *testing.T) {
	for _, statuses := range [][]securityassessment.AssertionStatus{
		{securityassessment.Failed, securityassessment.Unobserved, securityassessment.Passed},
		{securityassessment.Unobserved, securityassessment.Passed, securityassessment.Failed},
	} {
		assertions := make([]securityassessment.Assertion, len(statuses))
		for i, status := range statuses {
			assertions[i].Status = status
		}
		outcome, complete := securityAssertionOutcome(assertions)
		if outcome != "failed" || complete {
			t.Fatal("unobserved telemetry erased a confirmed security failure or became complete")
		}
	}
}

func TestSecurityQualificationRejectsMissingChangedAndUnqualifiedReceipts(t *testing.T) {
	p := securityProfileFixture(t)
	if _, err := p.load(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.QualificationPath, []byte(`{"route_qualified":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.load(); err == nil {
		t.Fatal("changed or unqualified receipt was accepted")
	}
	p.QualificationSHA256 = securityassessment.SHA256([]byte(`{"route_qualified":false}`))
	if _, err := p.load(); err == nil {
		t.Fatal("unqualified receipt was accepted after hash refresh")
	}
}

func TestPrivateSecuritySnapshotIsOutsideOrdinaryArtifactsAndOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	factory, err := artifacts.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	acts := &Activities{artifactFactory: factory, cfg: Config{Storage: StorageConfig{DataDir: dir}}}
	if err := acts.writeSecurityPrivate("run-test", "candidate/manifest.json", []byte(`{"private":"source inventory"}`)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "security-private", "run-test", "candidate", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private snapshot mode %v", info.Mode())
	}
	info, err = os.Stat(filepath.Join(dir, "security-private", "run-test"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("private run directory mode %v", info.Mode())
	}
	store, err := factory.ForRun("run-test")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := store.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("raw source appeared in ordinary artifact namespace: %v", paths)
	}
}

func TestMissingAssessmentExecutorIsExplicitIncompleteAdvice(t *testing.T) {
	factory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acts := &Activities{artifactFactory: factory, redactor: NewRedactor()}
	out, err := acts.AssessSecurity(context.Background(), SecurityAssessmentInput{RunID: "run-test", ProfileName: "tenant-http", ProfileConfig: securityProfileFixture(t), FunctionalPassed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.Status != "incomplete" || out.Summary.ErrorCode != "executor_unavailable" {
		t.Fatalf("missing executor became a pass: %+v", out)
	}
	store, _ := factory.ForRun("run-test")
	if ok, _ := store.Exists(ArtifactSecurityAssessment); !ok {
		t.Fatal("incomplete advisory was not retained")
	}
}

func TestSecurityAdvisoryPersistenceFailureCannotProduceCompleteReceipt(t *testing.T) {
	base, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acts := &Activities{artifactFactory: failingEvidenceFactory{base, ArtifactSecurityAssessment}, redactor: NewRedactor()}
	out, err := acts.AssessSecurity(context.Background(), SecurityAssessmentInput{RunID: "run-test", ProfileName: "tenant-http", ProfileConfig: securityProfileFixture(t), FunctionalPassed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.EvidenceError == "" || out.Summary.Status != "incomplete" {
		t.Fatal("lost advisory artifact became complete")
	}
}

func TestSecurityActivityOptionsNeverRetryAssessmentRequests(t *testing.T) {
	opts := securityAssessmentOptions()
	if opts.RetryPolicy == nil || opts.RetryPolicy.MaximumAttempts != 1 || opts.StartToCloseTimeout < 15*time.Minute ||
		!opts.WaitForCancellation || opts.HeartbeatTimeout <= 30*time.Second || opts.HeartbeatTimeout > 3*time.Minute {
		t.Fatalf("unsafe assessment activity policy: %+v", opts)
	}
	capture := securityCandidateCaptureOptions()
	if capture.RetryPolicy == nil || capture.RetryPolicy.MaximumAttempts != 1 || !capture.WaitForCancellation || capture.HeartbeatTimeout <= 30*time.Second || capture.HeartbeatTimeout >= capture.StartToCloseTimeout {
		t.Fatalf("unsafe candidate capture activity policy: %+v", capture)
	}
}

func TestSecurityAssessmentActivitySendsHeartbeat(t *testing.T) {
	factory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	acts := &Activities{artifactFactory: factory, redactor: NewRedactor()}
	for _, tc := range []struct {
		name                  string
		implementation, input any
	}{
		{"AssessSecurity", acts.AssessSecurity, SecurityAssessmentInput{RunID: "heartbeat-assessment"}},
		{"CaptureSecurityCandidate", acts.CaptureSecurityCandidate, CaptureSecurityCandidateInput{RunID: "heartbeat-capture"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestActivityEnvironment()
			env.RegisterActivity(tc.implementation)
			heartbeat := make(chan struct{}, 1)
			env.SetOnActivityHeartbeatListener(func(info *activity.Info, _ converter.EncodedValues) {
				if info.ActivityType.Name == tc.name {
					select {
					case heartbeat <- struct{}{}:
					default:
					}
				}
			})
			if _, err := env.ExecuteActivity(tc.implementation, tc.input); err != nil {
				t.Fatal(err)
			}
			select {
			case <-heartbeat:
			default:
				t.Fatal("security activity did not send its cancellation heartbeat")
			}
		})
	}
}
