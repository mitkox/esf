package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvaluateHealthDegradesWhenNoDeliveryFollowedTheSweep(t *testing.T) {
	t.Parallel()

	sweep := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	now := sweep.Add(2 * time.Minute)
	cases := []struct {
		name        string
		observation HealthObservation
		wantStatus  HealthStatus
		wantReason  string
	}{
		{
			name:        "no observations",
			observation: HealthObservation{Connector: "azdo"},
			wantStatus:  HealthUnknown,
			wantReason:  "no_observations",
		},
		{
			name: "no delivery since the sweep",
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(-time.Minute),
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "no_delivery_since_sweep",
		},
		{
			name: "delivery exactly at the sweep",
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep,
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "no_delivery_since_sweep",
		},
		{
			name: "delivery after the sweep",
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionActive,
			},
			wantStatus: HealthHealthy,
			wantReason: "delivery_after_sweep",
		},
		{
			name: "sweep without any delivery",
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "no_delivery_since_sweep",
		},
		{
			name: "delivery without a successful sweep",
			observation: HealthObservation{
				Connector: "azdo", LastDelivery: sweep.Add(time.Minute), Subscription: SubscriptionActive,
			},
			wantStatus: HealthDegraded,
			wantReason: "no_successful_sweep",
		},
		{
			name: "disabled subscription",
			observation: HealthObservation{
				Connector: "azdo", LastSuccessfulSweep: sweep, LastDelivery: sweep.Add(time.Minute),
				Subscription: SubscriptionDisabled,
			},
			wantStatus: HealthDegraded,
			wantReason: "subscription_disabled",
		},
		{
			name: "probationary subscription",
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
			got := EvaluateHealth(now, tc.observation)
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Fatalf("EvaluateHealth() = %s/%s, want %s/%s (%+v)",
					got.Status, got.Reason, tc.wantStatus, tc.wantReason, got)
			}
			if got.Connector != "azdo" || !got.EvaluatedAt.Equal(now) {
				t.Fatalf("unexpected health identity: %+v", got)
			}
			if got.LastSuccessfulSweep != nil && !got.LastSuccessfulSweep.Equal(sweep) {
				t.Fatalf("last successful sweep = %v", got.LastSuccessfulSweep)
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
	second := ConnectorHealth{Connector: "github", Status: HealthHealthy, Reason: "delivery_after_sweep", Subscription: SubscriptionActive}
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
