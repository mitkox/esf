//go:build integration

package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/assurance"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/tomlx"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// TestQualityCubeR2R3 exercises real microVMs, SQLite, Temporal replay and the
// worker's peer-authenticated socket. Harnesses are deterministic test fixtures.
func TestQualityCubeR2R3(t *testing.T) {
	for _, risk := range []string{"R2", "R3"} {
		t.Run(risk, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			fixture, _, req := qualityRuntimeFixture(t)
			cfg := fixture.Config
			cfg.Cube = cube.ConfigFromEnv()
			if cfg.Cube.APIURL == "" || cfg.Cube.TemplateID == "" {
				t.Fatal("CUBE_API_URL and an approved CUBE_TEMPLATE_ID are required")
			}
			cfg.Storage.DataDir = t.TempDir()
			cfg.Quality.Socket = filepath.Join(t.TempDir(), "q.sock")
			if err := os.Chmod(filepath.Dir(cfg.Quality.Socket), 0700); err != nil {
				t.Fatal(err)
			}
			cfg.Temporal.TaskQueue = fmt.Sprintf("quality-cube-%d", time.Now().UnixNano())
			cfg.Sandbox.BasePackages = []string{"git", "python3"}
			cfg.Limits.TotalTimeout = tomlx.FromStd(12 * time.Minute)
			cfg.Limits.AgentTimeout = tomlx.FromStd(2 * time.Minute)
			actorDir := t.TempDir()
			authorPath := "committed.txt"
			if risk == "R3" {
				authorPath = "auth/committed.txt"
			}
			author := fmt.Sprintf("#!/bin/sh\nset -eu\nif [ \"${1:-}\" = --version ]; then echo fixture-v1; exit 0; fi\nmkdir -p %s\nprintf 'committed by fixture\\n' > %s\ngit add -A\ngit -c user.name=Fixture -c user.email=fixture@test commit -qm fixture\nprintf '\\000\\001\\377' > binary.bin\nprintf '#!/bin/sh\\nexit 0\\n' > executable.sh\nchmod +x executable.sh\nln -s %s link\n", filepath.Dir(authorPath), authorPath, authorPath)
			reviewer := "#!/usr/bin/python3\nimport json,re,sys\nif '--version' in sys.argv: print('fixture-v1'); sys.exit(0)\np=open('/workspace/.factory/review-prompt.txt').read()\nd=re.findall(r'sha256:[a-f0-9]{64}',p)\nprint(json.dumps(dict(candidate_digest=d[0],plan_digest=d[1],verdict='approve',findings=[])))\n"
			for name, source := range map[string]string{"author": author, "reviewer": reviewer} {
				file := filepath.Join(actorDir, name)
				if err := os.WriteFile(file, []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
				h := cfg.Harnesses[name]
				h.Binary = file
				h.Executable = "/usr/local/bin/" + name
				cfg.Harnesses[name] = h
			}
			r, err := NewRuntime(ctx, RuntimeOptions{Config: cfg, Logger: slog.New(slog.DiscardHandler)})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(context.Background())
			if err = r.openQuality(); err != nil {
				t.Fatal(err)
			}
			c := temporalClientForTest(t)
			acts, err := r.Activities()
			if err != nil {
				t.Fatal(err)
			}
			w := worker.New(c, cfg.Temporal.TaskQueue, worker.Options{})
			w.RegisterWorkflow(SoftwareChangeWorkflow)
			w.RegisterWorkflow(CAPAWorkflow)
			w.RegisterActivity(acts)
			if err = w.Start(); err != nil {
				t.Fatal(err)
			}
			defer w.Stop()
			stop, err := r.serveQuality(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			req.RunID = fmt.Sprintf("cube-quality-%s-%d", strings.ToLower(risk), time.Now().UnixNano())
			req.SandboxTemplate = cfg.Cube.TemplateID
			admitted, err := r.admitQuality(ctx, assurance.Actor{ID: "fixture-requester", Kind: "human", Roles: []string{"submitter"}}, req)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("run %s", admitted.ID)
			qclient := NewQualityClient(cfg.QualitySocket())
			restarted := false
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				record, e := r.Quality.Store.Get(ctx, req.RunID)
				if e != nil {
					t.Fatal(e)
				}
				if record.Decision != nil {
					if record.Decision.Result != "approved" {
						t.Fatalf("quality rejected: %v", record.Decision.Reasons)
					}
					break
				}
				if record.Approval != nil && record.Approval.Status == "pending" {
					if !restarted {
						stop()
						w.Stop()
						if err = r.Quality.Store.Close(); err != nil {
							t.Fatal(err)
						}
						r.Quality = nil
						if err = r.openQuality(); err != nil {
							t.Fatal(err)
						}
						acts, err = r.Activities()
						if err != nil {
							t.Fatal(err)
						}
						w = worker.New(c, cfg.Temporal.TaskQueue, worker.Options{})
						w.RegisterWorkflow(SoftwareChangeWorkflow)
						w.RegisterWorkflow(CAPAWorkflow)
						w.RegisterActivity(acts)
						if err = w.Start(); err != nil {
							t.Fatal(err)
						}
						defer w.Stop()
						stop, err = r.serveQuality(ctx, c)
						if err != nil {
							t.Fatal(err)
						}
						defer stop()
						qclient = NewQualityClient(cfg.QualitySocket())
						restarted = true
					}
					infos, e := r.Provider.List(ctx)
					if e != nil {
						t.Fatal(e)
					}
					for _, sb := range infos {
						if sb.Metadata["run_id"] == req.RunID {
							t.Fatal("live sandbox during approval")
						}
					}
					var response assurance.Run
					e = qclient.Call(ctx, "POST", "/v1/runs/"+req.RunID+"/approval", assurance.ApprovalInput{Operation: "fixture-approve", RequestID: record.Approval.ID, CandidateDigest: record.Candidate.Digest, PlanDigest: record.Plan.Digest, Decision: "approve", Reason: "independently inspected controlled test evidence"}, &response)
					if e != nil {
						t.Fatal(e)
					}
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-ticker.C:
				}
			}
			record, err := r.Quality.Store.Get(ctx, req.RunID)
			if err != nil {
				t.Fatal(err)
			}
			var manifest RunManifest
			if err = json.Unmarshal(record.Manifest, &manifest); err != nil || manifest.FactoryResult != StateSucceeded {
				t.Fatalf("manifest: %+v %v", manifest, err)
			}
			// Get waits for the canonical result to propagate into the workflow outcome.
			if err = c.GetWorkflow(ctx, record.WorkflowID, record.ExecutionID).Get(ctx, &manifest); err != nil {
				t.Fatal(err)
			}
			history := c.GetWorkflowHistory(ctx, record.WorkflowID, record.ExecutionID, false, 0)
			replay, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{DataConverter: converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), testCodec(t, "new"))})
			if err != nil {
				t.Fatal(err)
			}
			replay.RegisterWorkflow(SoftwareChangeWorkflow)
			var events []*historypb.HistoryEvent
			for history.HasNext() {
				event, e := history.Next()
				if e != nil {
					t.Fatal(e)
				}
				events = append(events, event)
			}
			if err = replay.ReplayWorkflowHistory(nil, &historypb.History{Events: events}); err != nil {
				t.Fatalf("controlled history replay: %v", err)
			}
			changes, err := r.qualityChanges(ctx)
			if err != nil || len(changes) != 1 || changes[0].Status != ChangeDone {
				t.Fatalf("canonical changes: %+v %v", changes, err)
			}
		})
	}
}

// Produce a history using the pre-QMS command sequence, then replay it through
// the versioned public workflow. No production history is accessed.
func TestQualityReplaysLegacyHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r, _, req := qualityRuntimeFixture(t)
	r.Config.Quality = QualityConfig{}
	r.Config.Scopes = map[string]ScopeConfig{DefaultScope: {}}
	req.RunID = fmt.Sprintf("quality-legacy-replay-%d", time.Now().UnixNano())
	c := temporalClientForTest(t)
	queue := "queue-" + req.RunID
	acts, err := r.Activities()
	if err != nil {
		t.Fatal(err)
	}
	w := worker.New(c, queue, worker.Options{})
	w.RegisterWorkflowWithOptions(legacySoftwareChangeWorkflow, workflow.RegisterOptions{Name: WorkflowName})
	w.RegisterActivity(acts)
	if err = w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: WorkflowIDForRun(req.RunID), TaskQueue: queue}, WorkflowName, req)
	if err != nil {
		t.Fatal(err)
	}
	var result RunManifest
	if err = run.Get(ctx, &result); err != nil {
		t.Fatal(err)
	}
	history := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, 0)
	var events []*historypb.HistoryEvent
	for history.HasNext() {
		e, err := history.Next()
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	replay, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{DataConverter: converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), testCodec(t, "new"))})
	if err != nil {
		t.Fatal(err)
	}
	replay.RegisterWorkflow(SoftwareChangeWorkflow)
	if err = replay.ReplayWorkflowHistory(nil, &historypb.History{Events: events}); err != nil {
		t.Fatal(err)
	}
}
