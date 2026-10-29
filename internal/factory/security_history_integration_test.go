//go:build integration

package factory

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/agentharness"
	"github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/repository"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/securityassessment"
	"github.com/mitkox/esf/internal/verification"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"
)

// Execute inside the trusted validation Cube with an explicitly permitted real
// Temporal endpoint. These are new histories; they do not substitute for replay
// of a retained history produced by a pre-change factory release.
func TestSecurityTemporalNewHistoriesReplayEnabledAndDisabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := map[bool]string{false: "disabled", true: "enabled"}[enabled]
		t.Run(name, func(t *testing.T) {
			fake := sandbox.NewFake()
			baseExecute := wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
			var archive bytes.Buffer
			if err := tar.NewWriter(&archive).Close(); err != nil {
				t.Fatal(err)
			}
			inventory, err := json.Marshal(struct {
				Manifest      securityassessment.CandidateManifest `json:"manifest"`
				ArchiveSHA256 string                               `json:"archive_sha256"`
			}{securityassessment.CandidateManifest{Schema: securityassessment.CandidateSchema}, securityassessment.SHA256(archive.Bytes())})
			if err != nil {
				t.Fatal(err)
			}
			fake.ExecuteFunc = func(command sandbox.Command) (sandbox.Execution, error) {
				if command.Description != "freeze exact candidate bytes" {
					return baseExecute(command)
				}
				ids := fake.Live()
				if len(ids) != 1 {
					return sandbox.Execution{}, fmt.Errorf("expected one fake builder, got %d", len(ids))
				}
				sb, err := fake.Reattach(context.Background(), ids[0])
				if err != nil {
					return sandbox.Execution{}, err
				}
				if err := sb.WriteFile(context.Background(), command.Argv[5], archive.Bytes()); err != nil {
					return sandbox.Execution{}, err
				}
				return sandbox.Execution{}, sb.WriteFile(context.Background(), command.Argv[6], inventory)
			}
			cfg := hardenedDefaults()
			cfg.Storage.DataDir = t.TempDir()
			cfg.Cube.TemplateID = "tpl-history-test"
			cfg.Sandbox.BasePackages = []string{"git"}
			profile := securityProfileFixture(t)
			cfg.SecurityAssessmentProfiles = map[string]SecurityAssessmentProfileConfig{"tenant-http": profile}
			cfg.Scopes = map[string]ScopeConfig{DefaultScope: {SecurityAssessments: []string{"tenant-http"}}}
			mandatory := true
			cfg.Verification = map[string]verification.Profile{"default": {Name: "default", Steps: []verification.Step{{ID: "build", Argv: []string{"./build.sh"}, Mandatory: &mandatory}}}}
			harness, err := agentharness.NewGeneric(agentharness.Spec{Name: "integration-agent", Executable: "agent", Args: []string{"run"}, Timeout: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			registry := agentharness.NewRegistry()
			if err := registry.Register(harness); err != nil {
				t.Fatal(err)
			}
			store, err := artifacts.NewLocal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			telemetry, err := NewTelemetry(context.Background(), ObservabilityConfig{})
			if err != nil {
				t.Fatal(err)
			}
			acts, err := NewActivities(ActivitiesOptions{Provider: fake, Repositories: repository.New(nil), Harnesses: registry, Profiles: cfg.VerificationProfiles(), ArtifactFactory: store, Redactor: NewRedactor(), Telemetry: telemetry, Config: cfg, Logger: slog.New(slog.DiscardHandler)})
			if err != nil {
				t.Fatal(err)
			}
			c := temporalClientForTest(t)
			dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), testCodec(t, "new"))
			queue := fmt.Sprintf("security-history-%s-%d", name, time.Now().UnixNano())
			w := worker.New(c, queue, worker.Options{})
			w.RegisterWorkflowWithOptions(SoftwareChangeWorkflow, workflowRegistrationOptions())
			w.RegisterActivity(acts)
			if err := w.Start(); err != nil {
				t.Fatal(err)
			}
			defer w.Stop()
			req := baseRequest(t)
			req.RunID = queue
			req.AgentHarness = "integration-agent"
			if enabled {
				req.SecurityAssessmentProfile = "tenant-http"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: WorkflowIDForRun(req.RunID), TaskQueue: queue}, SoftwareChangeWorkflow, req)
			if err != nil {
				t.Fatal(err)
			}
			var manifest RunManifest
			if err := run.Get(ctx, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.FactoryResult != StateSucceeded || !manifest.CleanupResult.Verified || len(fake.Live()) != 0 {
				t.Fatalf("workflow failed or leaked builder: result=%s cleanup=%+v error=%s", manifest.FactoryResult, manifest.CleanupResult, manifest.Error)
			}
			if enabled {
				if manifest.SecurityAssessment == nil || manifest.SecurityAssessment.ErrorCode != "executor_unavailable" || manifest.SecurityAssessment.Status != "incomplete" || manifest.SecurityAssessment.CandidateDigest == "" {
					t.Fatalf("enabled path did not capture candidate and retain explicit unavailable advice: %+v", manifest.SecurityAssessment)
				}
			} else if manifest.SecurityAssessment != nil {
				t.Fatalf("disabled path acquired security advice: %+v", manifest.SecurityAssessment)
			}
			history := &historypb.History{}
			iter := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			activities := map[string]int{}
			marker := false
			for iter.HasNext() {
				event, err := iter.Next()
				if err != nil {
					t.Fatal(err)
				}
				history.Events = append(history.Events, event)
				if scheduled := event.GetActivityTaskScheduledEventAttributes(); scheduled != nil {
					activities[scheduled.ActivityType.Name]++
				}
				if recorded := event.GetMarkerRecordedEventAttributes(); recorded != nil && recorded.MarkerName == "Version" {
					var changeID string
					if details := recorded.Details["change-id"]; details != nil {
						if err := dc.FromPayloads(details, &changeID); err != nil {
							t.Fatal(err)
						}
						marker = marker || changeID == securityAssessmentVersion
					}
				}
			}
			if len(history.Events) == 0 || history.Events[len(history.Events)-1].EventType != enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED {
				t.Fatal("server history is not a complete successful execution")
			}
			expectedActivities := 0
			if enabled {
				expectedActivities = 1
			}
			if marker != enabled || activities["CaptureSecurityCandidate"] != expectedActivities || activities["AssessSecurity"] != expectedActivities {
				t.Fatalf("actual history did not follow requested path: enabled=%v marker=%v activities=%v", enabled, marker, activities)
			}
			data, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(history)
			if err != nil {
				t.Fatal(err)
			}
			dir := os.Getenv("SECURITY_NEW_HISTORY_DIR")
			if dir == "" {
				dir = t.TempDir()
			}
			if !filepath.IsAbs(dir) {
				t.Fatal("SECURITY_NEW_HISTORY_DIR must be absolute and private")
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(dir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
				t.Fatal("history output directory must be owner-only")
			}
			file := filepath.Join(dir, queue+".json")
			f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(data); err != nil {
				f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{DataConverter: dc, FailureConverter: temporal.NewDefaultFailureConverter(temporal.DefaultFailureConverterOptions{DataConverter: dc, EncodeCommonAttributes: true})})
			if err != nil {
				t.Fatal(err)
			}
			replayer.RegisterWorkflowWithOptions(SoftwareChangeWorkflow, workflowRegistrationOptions())
			if err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, file); err != nil {
				t.Fatalf("new %s server history is incompatible: %v", name, err)
			}
			t.Logf("real Temporal replay passed: profile=%s workflow_id=%s run_id=%s events=%d security_marker=%v capture_activities=%d assessment_activities=%d sha256=%s history=%s", name, run.GetID(), run.GetRunID(), len(history.Events), marker, activities["CaptureSecurityCandidate"], activities["AssessSecurity"], securityassessment.SHA256(data), file)
		})
	}
}
