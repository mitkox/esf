package factory

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/assurance"
)

func TestQualityProviderAcknowledgmentDurabilityAndBinding(t *testing.T) {
	for _, scenario := range []string{"timely", "late", "mismatch", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			runtime, _, req := qualityRuntimeFixture(t)
			control := assurance.Control{ID: "external-ack", Type: "provider-ack", Reference: "audit", ProviderTimeout: "1h"}
			policy := runtime.Quality.Policies["qms"]
			policy.Metadata.Version = "2"
			policy.Spec.Gates = append(policy.Spec.Gates, control)
			runtime.Quality.Policies["qms"] = policy
			record, err := runtime.admitQuality(ctx, assurance.Actor{ID: "uid:1", Kind: "human"}, req)
			if err != nil {
				t.Fatal(err)
			}
			record, err = runtime.Quality.Store.Bind(ctx, record.ID, record.AdmissionID, record.RequestDigest, record.WorkflowID, "execution")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := assurance.Evaluate(record.Snapshot, []string{"server.go"})
			if err != nil {
				t.Fatal(err)
			}
			candidate := assurance.Candidate{Digest: assurance.Digest([]byte("candidate")), Paths: []string{"server.go"}}
			record, err = runtime.Quality.Store.Transition(ctx, record.ID, "candidate", candidate, func(r *assurance.Run, _ time.Time) error { r.Candidate = &candidate; r.Plan = &plan; return nil })
			if err != nil {
				t.Fatal(err)
			}
			acts, err := runtime.Activities()
			if err != nil {
				t.Fatal(err)
			}
			input := QualityGateInput{record.ID, "execution", control, 1}
			pending, err := acts.QualityProviderCheck(ctx, input)
			if err != nil || pending.Ready || pending.Expired {
				t.Fatalf("no acquisition window: %+v %v", pending, err)
			}
			// Lost activity acknowledgment returns the same persisted request/deadline.
			repeated, err := acts.QualityProviderCheck(ctx, input)
			if err != nil || repeated.Deadline != pending.Deadline || repeated.Reference != record.ID+".audit" {
				t.Fatal("request was not stable", err)
			}
			if scenario == "late" || scenario == "missing" {
				runtime.Quality.Store.Now = func() time.Time { return pending.Deadline.Add(time.Second) }
			}
			if scenario != "missing" {
				digest := candidate.Digest
				if scenario == "mismatch" {
					digest = "different"
				}
				body := map[string]any{"run_id": record.ID, "candidate_digest": digest, "plan_digest": plan.Digest, "acknowledged": true}
				if err = runtime.Quality.Store.Import(ctx, "acknowledgment", pending.Reference, body); err != nil {
					t.Fatal(err)
				}
			}
			// A timely response remains eligible after a restart or delayed wake.
			runtime.Quality.Store.Now = func() time.Time { return pending.Deadline.Add(2 * time.Second) }
			state, err := acts.QualityProviderCheck(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "timely" && !state.Ready {
				t.Fatal("lost timely response")
			}
			gate, err := acts.QualityGate(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if (gate.Status == "passed") != (scenario == "timely") {
				t.Fatalf("scenario %s: %+v", scenario, gate)
			}
			if gate.Status == "passed" {
				data, err := runtime.Quality.Content.Read(gate.Evidence[0].Digest)
				if err != nil || !json.Valid(data) {
					t.Fatal("acknowledgment receipt missing", err)
				}
			}
		})
	}
}
