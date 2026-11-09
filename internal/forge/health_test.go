package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvaluateHealth(t *testing.T) {
	t.Parallel()

	sweep := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name        string
		now         time.Time
		observation HealthObservation
		wantStatus  HealthStatus
		wantReason  string
		wantDetail  string
	}{
		{
			name:        "no observations",
			now:         sweep,
			observation: HealthObservation{Connector: "azdo"},
			wantStatus:  HealthUnknown,
			wantReason:  "no_observations",
		},
		{
			name: "fresh sweep with no delivery ever is healthy",
			now:  sweep.Add(time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep,
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthHealthy,
			wantReason: "sweep_only",
		},
		{
			name: "delivery within the idle grace",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep,
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthHealthy,
			wantReason: "recent_delivery",
		},
		{
			name: "delivery within the idle grace after a newer sweep",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep.Add(time.Minute), LastDelivery: sweep,
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthHealthy,
			wantReason: "recent_delivery",
		},
		{
			name: "delivery after the sweep",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthHealthy,
			wantReason: "recent_delivery",
		},
		{
			name: "delivery older than the freshness window",
			now:  sweep.Add(5 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep.Add(4 * time.Minute), LastDelivery: sweep,
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "no_recent_delivery",
		},
		{
			name: "stale sweep with a newer delivery",
			now:  sweep.Add(4 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "stale_sweep",
		},
		{
			name: "configured sweep interval sizes the freshness window",
			now:  sweep.Add(20 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", SweepInterval: 10 * time.Minute,
				LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(-5 * time.Minute),
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthHealthy,
			wantReason: "recent_delivery",
		},
		{
			name: "delivery without a successful sweep",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastDelivery: sweep.Add(time.Minute), Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "no_successful_sweep",
		},
		{
			name: "failed sweep attempt after a recent delivery",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", Event: HealthEventSweep, Successful: false,
				Detail:              "reconciliation sweep failed: upstream 503",
				LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "sweep_failed",
			wantDetail: "upstream 503",
		},
		{
			name: "failed sweep attempt with no prior observations",
			now:  sweep,
			observation: HealthObservation{
				Connector: "azdo", Event: HealthEventSweep, Successful: false,
				Detail: "reconciliation sweep failed", Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "sweep_failed",
		},
		{
			name: "failed delivery attempt after a recent delivery",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", Event: HealthEventDelivery, Successful: false,
				Detail:              "webhook delivery failed with status 502",
				LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "delivery_failed",
			wantDetail: "webhook delivery failed with status 502",
		},
		{
			// A failed delivery with no prior delivery is the case a stalled
			// intake reaches: the fresh sweep alone must not report healthy.
			name: "failed delivery attempt with no prior delivery",
			now:  sweep.Add(time.Minute),
			observation: HealthObservation{
				Connector: "azdo", Event: HealthEventDelivery, Successful: false,
				Detail:              "intake delivery failed",
				LastSuccessfulSweep: sweep,
				Subscription:        SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "delivery_failed",
			wantDetail: "intake delivery failed",
		},
		{
			name: "failed delivery attempt without detail",
			now:  sweep.Add(time.Minute),
			observation: HealthObservation{
				Connector: "azdo", Event: HealthEventDelivery, Successful: false,
				LastSuccessfulSweep: sweep,
				Subscription:        SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "delivery_failed",
			wantDetail: "delivery attempt failed",
		},
		{
			name: "disabled subscription",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionDisabled,
			},
			wantStatus: HealthDegraded,
			wantReason: "subscription_disabled",
		},
		{
			name: "probationary subscription",
			now:  sweep.Add(2 * time.Minute),
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionProbationary,
			},
			wantStatus: HealthDegraded,
			wantReason: "subscription_probationary",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EvaluateHealth(tc.now, tc.observation)
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Fatalf("EvaluateHealth() = %s/%s, want %s/%s (%+v)",
					got.Status, got.Reason, tc.wantStatus, tc.wantReason, got)
			}
			if got.Connector != "azdo" || !got.EvaluatedAt.Equal(tc.now) {
				t.Fatalf("unexpected health identity: %+v", got)
			}
			if tc.observation.LastSuccessfulSweep.IsZero() {
				if got.LastSuccessfulSweep != nil {
					t.Fatalf("last successful sweep = %v, want none", got.LastSuccessfulSweep)
				}
			} else if got.LastSuccessfulSweep == nil || !got.LastSuccessfulSweep.Equal(tc.observation.LastSuccessfulSweep) {
				t.Fatalf("last successful sweep = %v, want %v", got.LastSuccessfulSweep, tc.observation.LastSuccessfulSweep)
			}
			if tc.wantDetail != "" && !strings.Contains(got.Detail, tc.wantDetail) {
				t.Fatalf("health detail = %q, want it to contain %q", got.Detail, tc.wantDetail)
			}
			if tc.wantReason == "subscription_disabled" {
				if got.Subscription != SubscriptionDisabled || !strings.Contains(got.Detail, "disabled") {
					t.Fatalf("disabled health does not name the state: %+v", got)
				}
			}
		})
	}
}

func TestParseSubscriptionState(t *testing.T) {
	t.Parallel()

	cases := map[string]SubscriptionState{
		"":             SubscriptionUnknown,
		"unknown":      SubscriptionUnknown,
		"active":       SubscriptionActive,
		"ACTIVE":       SubscriptionActive,
		"disabled":     SubscriptionDisabled,
		"probationary": SubscriptionProbationary,
		" probation ":  SubscriptionProbationary,
		"not-a-state":  SubscriptionUnknown,
	}
	for input, want := range cases {
		if got := ParseSubscriptionState(input); got != want {
			t.Fatalf("ParseSubscriptionState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHealthSnapshotRoundTripIsBoundedAndPrivate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sweep := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	first := ConnectorHealth{
		Connector: "azdo", Status: HealthDegraded, Reason: "subscription_disabled",
		Detail: strings.Repeat("x", 4096), Subscription: SubscriptionDisabled,
		LastSuccessfulSweep: &sweep, QueueDepth: map[string]int{"payments": 3, "platform": 0},
	}
	if err := SaveConnectorHealth(root, first); err != nil {
		t.Fatalf("SaveConnectorHealth: %v", err)
	}
	second := ConnectorHealth{Connector: "github", Status: HealthHealthy, Reason: "recent_delivery", Subscription: SubscriptionActive}
	if err := SaveConnectorHealth(root, second); err != nil {
		t.Fatalf("SaveConnectorHealth second: %v", err)
	}

	snapshot, err := LoadHealthSnapshot(root)
	if err != nil {
		t.Fatalf("LoadHealthSnapshot: %v", err)
	}
	if len(snapshot.Connectors) != 2 {
		t.Fatalf("connectors = %d, want 2", len(snapshot.Connectors))
	}
	if snapshot.Connectors[0].Connector != "azdo" || snapshot.Connectors[1].Connector != "github" {
		t.Fatalf("snapshot is not sorted by connector: %+v", snapshot.Connectors)
	}
	if len(snapshot.Connectors[0].Detail) > maxHealthTextBytes {
		t.Fatalf("detail was not bounded: %d bytes", len(snapshot.Connectors[0].Detail))
	}
	info, err := os.Stat(filepath.Join(root, HealthDirName, healthSnapshotFileName))
	if err != nil {
		t.Fatalf("stat snapshot: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %o, want 0600", info.Mode().Perm())
	}

	missing, err := LoadHealthSnapshot(t.TempDir())
	if err != nil {
		t.Fatalf("LoadHealthSnapshot missing: %v", err)
	}
	if len(missing.Connectors) != 0 {
		t.Fatalf("missing snapshot returned connectors: %+v", missing.Connectors)
	}
}

// TestSaveConnectorHealthReplacesTheLatestObservation proves the documented
// durable contract is latest-attempt-wins: a later observation for the same
// connector replaces the previous degraded entry, including a successful sweep
// after a failed delivery. The failed attempt is not held until a successful
// delivery arrives.
func TestSaveConnectorHealthReplacesTheLatestObservation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	failedDelivery := ConnectorHealth{
		Connector: "azdo", Status: HealthDegraded, Reason: "delivery_failed",
		Detail: "webhook delivery failed with status 502", Subscription: SubscriptionActive,
	}
	if err := SaveConnectorHealth(root, failedDelivery); err != nil {
		t.Fatalf("SaveConnectorHealth failed delivery: %v", err)
	}
	sweepOnly := ConnectorHealth{
		Connector: "azdo", Status: HealthHealthy, Reason: "sweep_only",
		Detail: "the reconciliation sweep is current and no delivery has been observed yet", Subscription: SubscriptionActive,
	}
	if err := SaveConnectorHealth(root, sweepOnly); err != nil {
		t.Fatalf("SaveConnectorHealth later sweep: %v", err)
	}

	snapshot, err := LoadHealthSnapshot(root)
	if err != nil {
		t.Fatalf("LoadHealthSnapshot: %v", err)
	}
	if len(snapshot.Connectors) != 1 || snapshot.Connectors[0].Connector != "azdo" {
		t.Fatalf("snapshot = %+v, want one replaced azdo entry", snapshot.Connectors)
	}
	if stored := snapshot.Connectors[0]; stored.Status != HealthHealthy || stored.Reason != "sweep_only" {
		t.Fatalf("stored health = %s/%s, want the latest observation", stored.Status, stored.Reason)
	}
}

func TestSaveConnectorHealthRejectsUnsafeIdentity(t *testing.T) {
	t.Parallel()

	for _, report := range []ConnectorHealth{
		{Connector: ""},
		{Connector: "bad/name"},
		{Connector: "bad\nname"},
	} {
		if err := SaveConnectorHealth(t.TempDir(), report); err == nil {
			t.Fatalf("SaveConnectorHealth(%q) accepted an unsafe identity", report.Connector)
		}
	}
}
