package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mitkox/esf/internal/assurance"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

type QualitySummary struct {
	RepositoryID      string              `json:"repository_id"`
	SnapshotDigest    string              `json:"snapshot_digest"`
	CandidateDigest   string              `json:"candidate_digest,omitempty"`
	PlanDigest        string              `json:"plan_digest,omitempty"`
	Risk              string              `json:"risk,omitempty"`
	Decision          *assurance.Decision `json:"decision,omitempty"`
	Missing           []string            `json:"missing,omitempty"`
	AttestationDigest string              `json:"attestation_digest,omitempty"`
}

func summarizeQuality(r assurance.Run) *QualitySummary {
	s := &QualitySummary{RepositoryID: r.Snapshot.RepositoryID, SnapshotDigest: r.Snapshot.Digest, Decision: r.Decision, AttestationDigest: r.AttestationDigest, Missing: assurance.MissingControls(r)}
	if r.Candidate != nil {
		s.CandidateDigest = r.Candidate.Digest
	}
	if r.Plan != nil {
		s.PlanDigest = r.Plan.Digest
		s.Risk = r.Plan.Risk
	}
	return s
}

type QualityRuntime struct {
	Store        *assurance.Store
	Content      *assurance.Content
	Policies     map[string]assurance.Policy
	metricSeries map[string]bool
	Classifier   assurance.RiskClassifier
}

func (r *Runtime) openQuality() error {
	if !r.Config.Quality.Enabled() || r.Quality != nil {
		return nil
	}
	root := filepath.Join(r.Artifacts.Root(), "quality")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	if err := qualityCheckDirectory(root); err != nil {
		return err
	}
	if err := os.Chmod(root, 0700); err != nil {
		return err
	}
	store, err := assurance.OpenStore(filepath.Join(root, "quality.db"))
	if err != nil {
		return err
	}
	content, err := assurance.OpenContent(filepath.Join(root, "objects"))
	if err != nil {
		store.Close()
		return err
	}
	policies, err := r.Config.qualityPolicies()
	if err != nil {
		store.Close()
		return err
	}
	var active []assurance.Policy
	for _, p := range policies {
		body, _ := json.Marshal(p)
		if qualityJSONHasSecrets(r.Redactor, body) {
			store.Close()
			return fmt.Errorf("policy %s contains protected secret material", p.Metadata.Name)
		}
		active = append(active, p)
	}
	if err = store.RegisterPolicies(context.Background(), active); err != nil {
		store.Close()
		return err
	}
	r.Quality = &QualityRuntime{Store: store, Content: content, Policies: policies, Classifier: assurance.DeterministicClassifier{}}
	return nil
}

func (r *Runtime) qualityOutbox(ctx context.Context, c client.Client) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		r.drainQualityOutbox(ctx, c)
		r.observeQuality(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *Runtime) drainQualityOutbox(ctx context.Context, c client.Client) {
	entries, err := r.Quality.Store.Pending(ctx)
	if err != nil {
		r.Log.Error("quality outbox read failed", "error", err)
		return
	}
	for _, o := range entries {
		var err error
		switch o.Kind {
		case "start-run":
			var record assurance.Run
			record, err = r.Quality.Store.Get(ctx, o.Aggregate)
			if err == nil {
				var req RunRequest
				err = json.Unmarshal(record.Request, &req)
				if err == nil {
					req.AdmissionID = record.AdmissionID
					_, err = c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: record.WorkflowID, TaskQueue: r.Config.Temporal.TaskQueue, WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, WorkflowName, req)
				}
			}
		case "start-capa":
			_, err = c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "factory-capa-" + o.Aggregate, TaskQueue: r.Config.Temporal.TaskQueue, WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE}, CAPAWorkflowName, o.Aggregate)
		case "approval-wake", "provider-wake":
			var payload map[string]string
			err = json.Unmarshal(o.Payload, &payload)
			if err == nil {
				record, e := r.Quality.Store.Get(ctx, payload["run_id"])
				err = e
				if err == nil {
					if record.Decision == nil {
						err = c.SignalWorkflow(ctx, record.WorkflowID, record.ExecutionID, QualityWakeSignal, nil)
					}
				}
			}
		case "export-run":
			var record assurance.Run
			record, err = r.Quality.Store.Get(ctx, o.Aggregate)
			if err == nil {
				store, e := r.Artifacts.ForRun(record.ID)
				err = e
				if err == nil {
					err = store.Write(ArtifactRun, record.Manifest)
					if err == nil && len(record.Attestation) > 0 {
						err = store.Write("quality/attestation.json", record.Attestation)
					}
					if err == nil && record.Candidate != nil {
						var b []byte
						b, err = r.Quality.Content.Read(record.Candidate.PatchDigest)
						if err == nil {
							err = store.Write(ArtifactPatch, b)
						}
					}
				}
			}
		case "publish-quality":
			var record assurance.Run
			record, err = r.Quality.Store.Get(ctx, o.Aggregate)
			if err == nil {
				err = (assurance.LocalProvider{Store: r.Quality.Store}).CreateQualityRecord(ctx, record.ID, record)
			}
		default:
			err = fmt.Errorf("unsupported outbox operation %s", o.Kind)
		}
		if isAlreadyStarted(err) && (o.Kind == "start-run" || o.Kind == "start-capa") {
			if o.Kind == "start-capa" {
				err = nil
			} else {
				record, e := r.Quality.Store.Get(ctx, o.Aggregate)
				if e != nil {
					err = e
				} else if record.ExecutionID == "" {
					err = fmt.Errorf("workflow ID already exists; awaiting matching durable admission binding")
				} else {
					desc, e := c.DescribeWorkflowExecution(ctx, record.WorkflowID, record.ExecutionID)
					if e != nil {
						err = e
					} else if desc.WorkflowExecutionInfo.Execution.RunId != record.ExecutionID {
						err = fmt.Errorf("workflow execution binding conflict")
					} else {
						err = nil
					}
				}
			}
		}
		problem := ""
		if err != nil {
			problem = r.Redactor.Redact(err.Error())
			r.Log.Warn("quality outbox pending", "kind", o.Kind, "aggregate", o.Aggregate, "error", problem)
		}
		if e := r.Quality.Store.CompleteOutbox(ctx, o.ID, problem); e != nil {
			r.Log.Error("quality outbox acknowledgement failed", "error", e)
		}
	}
}
