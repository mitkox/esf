package factory

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/notify"
)

func newManualMetrics(t *testing.T) (*Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	metrics, err := newMetrics(provider)
	if err != nil {
		t.Fatalf("newMetrics: %v", err)
	}
	return metrics, reader
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return collected
}

func metricByName(t *testing.T, collected metricdata.ResourceMetrics, name string) metricdata.Metrics {
	t.Helper()
	for _, scope := range collected.ScopeMetrics {
		for _, candidate := range scope.Metrics {
			if candidate.Name == name {
				return candidate
			}
		}
	}
	t.Fatalf("metric %q was not recorded", name)
	return metricdata.Metrics{}
}

func gaugeValueFor(t *testing.T, data metricdata.Aggregation, key, value string) int64 {
	t.Helper()
	gauge, ok := data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("metric data = %T, want metricdata.Gauge[int64]", data)
	}
	for _, point := range gauge.DataPoints {
		got, ok := point.Attributes.Value(attribute.Key(key))
		if ok && got.AsString() == value {
			return point.Value
		}
	}
	t.Fatalf("no gauge datapoint with %s=%q", key, value)
	return 0
}

func sumValueFor(t *testing.T, data metricdata.Aggregation, key, value string) int64 {
	t.Helper()
	sum, ok := data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric data = %T, want metricdata.Sum[int64]", data)
	}
	for _, point := range sum.DataPoints {
		got, ok := point.Attributes.Value(attribute.Key(key))
		if ok && got.AsString() == value {
			return point.Value
		}
	}
	t.Fatalf("no sum datapoint with %s=%q", key, value)
	return 0
}

func TestMetricsRecordConnectorHealthAndFailureHooks(t *testing.T) {
	metrics, reader := newManualMetrics(t)
	ctx := context.Background()
	sweep := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	delivery := sweep.Add(time.Minute)

	metrics.RecordConnectorSweep(ctx, "azdo", sweep)
	metrics.RecordConnectorDelivery(ctx, "azdo", delivery)
	metrics.RecordSubscriptionState(ctx, "azdo", forge.SubscriptionDisabled)
	metrics.RecordPublicationFailure(ctx, "forge_failed")
	metrics.RecordEvidenceFailure(ctx, "evidence_mismatch")
	metrics.RecordScopeQueueDepth(ctx, "payments", 4)

	collected := collectMetrics(t, reader)
	if got := gaugeValueFor(t, metricByName(t, collected, "factory_connector_last_successful_sweep_timestamp_seconds").Data, AttrConnector, "azdo"); got != sweep.Unix() {
		t.Fatalf("last successful sweep = %d, want %d", got, sweep.Unix())
	}
	if got := gaugeValueFor(t, metricByName(t, collected, "factory_connector_last_delivery_timestamp_seconds").Data, AttrConnector, "azdo"); got != delivery.Unix() {
		t.Fatalf("last delivery = %d, want %d", got, delivery.Unix())
	}
	subscription := metricByName(t, collected, "factory_connector_subscription_state")
	if got := gaugeValueFor(t, subscription.Data, AttrSubscriptionState, string(forge.SubscriptionDisabled)); got != 1 {
		t.Fatalf("subscription disabled gauge = %d, want 1", got)
	}
	if got := gaugeValueFor(t, subscription.Data, AttrSubscriptionState, string(forge.SubscriptionActive)); got != 0 {
		t.Fatalf("subscription active gauge = %d, want 0", got)
	}
	if got := sumValueFor(t, metricByName(t, collected, "factory_publication_failures_total").Data, AttrFailureClass, "forge_failed"); got != 1 {
		t.Fatalf("publication failures = %d, want 1", got)
	}
	if got := sumValueFor(t, metricByName(t, collected, "factory_evidence_failures_total").Data, AttrFailureClass, "evidence_mismatch"); got != 1 {
		t.Fatalf("evidence failures = %d, want 1", got)
	}
	if got := gaugeValueFor(t, metricByName(t, collected, "factory_scope_queue_depth").Data, AttrScope, "payments"); got != 4 {
		t.Fatalf("scope queue depth = %d, want 4", got)
	}

	var nilMetrics *Metrics
	nilMetrics.RecordConnectorSweep(ctx, "azdo", sweep)
	nilMetrics.RecordConnectorDelivery(ctx, "azdo", delivery)
	nilMetrics.RecordSubscriptionState(ctx, "azdo", forge.SubscriptionActive)
	nilMetrics.RecordPublicationFailure(ctx, "forge_failed")
	nilMetrics.RecordEvidenceFailure(ctx, "signing_failed")
	nilMetrics.RecordScopeQueueDepth(ctx, "payments", 1)
}

// TestReportConnectorHealthAlertsOnceWhileDegraded proves a connector that
// stays degraded does not repeat its alert on every observation, while a changed
// reason and a new degradation after recovery both alert.
func TestReportConnectorHealthAlertsOnceWhileDegraded(t *testing.T) {
	var mu sync.Mutex
	var received []notify.Event
	webhook := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			t.Errorf("decode alert: %v", err)
		}
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		response.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	artifactFactory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	metrics, _ := newManualMetrics(t)
	acts := &Activities{
		artifactFactory: artifactFactory,
		redactor:        NewRedactor(),
		telemetry:       &Telemetry{metrics: metrics},
		cfg:             Config{Hardening: HardeningConfig{AlertWebhookURL: webhook.URL}},
		log:             slog.New(slog.DiscardHandler),
	}
	now := time.Now().UTC()
	disabled := ConnectorHealthInput{
		Connector:           "azdo",
		LastSuccessfulSweep: now.Add(-time.Minute),
		LastDelivery:        now.Add(-2 * time.Minute),
		Subscription:        "disabled",
	}
	failedSweep := ConnectorHealthInput{
		Connector:           "azdo",
		SweepInterval:       time.Minute,
		Event:               "sweep",
		Successful:          false,
		Detail:              "reconciliation sweep failed",
		LastSuccessfulSweep: now.Add(-time.Minute),
		LastDelivery:        now.Add(-2 * time.Minute),
		Subscription:        "active",
	}
	healthy := ConnectorHealthInput{
		Connector:           "azdo",
		SweepInterval:       time.Minute,
		Event:               "sweep",
		Successful:          true,
		LastSuccessfulSweep: now.Add(-time.Minute),
		LastDelivery:        now.Add(-2 * time.Minute),
		Subscription:        "active",
	}

	first, err := acts.ReportConnectorHealth(context.Background(), disabled)
	if err != nil {
		t.Fatalf("first ReportConnectorHealth: %v", err)
	}
	if first.Alert == nil {
		t.Fatal("the first degraded observation raised no alert")
	}
	second, err := acts.ReportConnectorHealth(context.Background(), disabled)
	if err != nil {
		t.Fatalf("second ReportConnectorHealth: %v", err)
	}
	if second.Alert != nil {
		t.Fatalf("an unchanged degraded observation raised another alert: %+v", second.Alert)
	}
	changed, err := acts.ReportConnectorHealth(context.Background(), failedSweep)
	if err != nil {
		t.Fatalf("changed-reason ReportConnectorHealth: %v", err)
	}
	if changed.Alert == nil {
		t.Fatal("a changed degradation reason raised no alert")
	}
	recovered, err := acts.ReportConnectorHealth(context.Background(), healthy)
	if err != nil {
		t.Fatalf("recovery ReportConnectorHealth: %v", err)
	}
	if recovered.Alert != nil || recovered.Health.Status != forge.HealthHealthy {
		t.Fatalf("recovery = %+v, want a healthy observation without an alert", recovered)
	}
	regressed, err := acts.ReportConnectorHealth(context.Background(), failedSweep)
	if err != nil {
		t.Fatalf("regression ReportConnectorHealth: %v", err)
	}
	if regressed.Alert == nil {
		t.Fatal("a degradation after recovery raised no alert")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("alerts delivered = %d, want three distinct degradations", len(received))
	}
	if received[0].Kind != AlertConnectorDegraded || received[2].Kind != AlertConnectorDegraded {
		t.Fatalf("alert kinds = %q/%q", received[0].Kind, received[2].Kind)
	}
}

// TestReportConnectorHealthEmitsQueueDepthFromIntakeRecords proves the
// documented queue-depth gauge is produced from durable intake records, grouped
// by their resolved scope, and that legacy records without a scope contribute no
// series.
func TestReportConnectorHealthEmitsQueueDepthFromIntakeRecords(t *testing.T) {
	artifactFactory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	store, err := openIntakeStore(artifactFactory.Root())
	if err != nil {
		t.Fatalf("openIntakeStore: %v", err)
	}
	for _, item := range []struct {
		id       string
		revision string
		scope    string
		outcome  string
	}{
		{id: "1", revision: "1", scope: "payments"},
		{id: "2", revision: "2", scope: "payments"},
		{id: "3", revision: "3", scope: "platform"},
		{id: "4", revision: "4", scope: "payments", outcome: intakeRecordSucceeded},
		{id: "5", revision: "5"},
	} {
		record := intakeRecord{
			Connector:    "azdo",
			Repository:   "https://dev.azure.com/acme/payments/_git/payments",
			WorkItemID:   item.id,
			Revision:     item.revision,
			SnapshotHash: "sha256:" + strings.Repeat("a", 64),
			ChangeID:     "workitem-change-" + item.id,
			RunID:        "wi-run-" + item.id,
			ActivationID: "activation-" + item.id,
			Scope:        item.scope,
		}
		claim, err := store.Claim(context.Background(), record)
		if err != nil || !claim.Accepted {
			t.Fatalf("claim work item %s = %#v, %v", item.id, claim, err)
		}
		if item.outcome == "" {
			continue
		}
		if err := store.RecordOutcome(context.Background(), WorkItemOutcomeInput{
			Connector: record.Connector, Repository: record.Repository,
			WorkItemID: record.WorkItemID, Revision: record.Revision,
			Status: item.outcome,
		}); err != nil {
			t.Fatalf("record work item %s outcome: %v", item.id, err)
		}
	}

	metrics, reader := newManualMetrics(t)
	reporter, err := NewConnectorHealthReporter(Default(), artifactFactory, &Telemetry{metrics: metrics}, nil)
	if err != nil {
		t.Fatalf("NewConnectorHealthReporter: %v", err)
	}
	depths, err := reporter.QueueDepth(context.Background())
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depths["payments"] != 2 || depths["platform"] != 1 || len(depths) != 2 {
		t.Fatalf("queue depth = %v, want two payments and one platform and no legacy series", depths)
	}

	now := time.Now().UTC()
	out, err := reporter.Report(context.Background(), ConnectorHealthInput{
		Connector:           "azdo",
		SweepInterval:       time.Minute,
		Event:               "sweep",
		Successful:          true,
		LastSuccessfulSweep: now,
		LastDelivery:        now.Add(-time.Second),
		Subscription:        "active",
		QueueDepth:          depths,
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if out.Health.Status != forge.HealthHealthy {
		t.Fatalf("health = %#v, want healthy", out.Health)
	}
	if out.Health.QueueDepth["payments"] != 2 || out.Health.QueueDepth["platform"] != 1 {
		t.Fatalf("health queue depth = %v", out.Health.QueueDepth)
	}

	gauge := metricByName(t, collectMetrics(t, reader), "factory_scope_queue_depth")
	data, ok := gauge.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("queue depth data = %T, want metricdata.Gauge[int64]", gauge.Data)
	}
	if len(data.DataPoints) != 2 {
		t.Fatalf("queue depth datapoints = %d, want one per scoped group", len(data.DataPoints))
	}
	if got := gaugeValueFor(t, gauge.Data, AttrScope, "payments"); got != 2 {
		t.Fatalf("payments queue depth = %d, want 2", got)
	}
	if got := gaugeValueFor(t, gauge.Data, AttrScope, "platform"); got != 1 {
		t.Fatalf("platform queue depth = %d, want 1", got)
	}
}

func TestReportConnectorHealthRecordsAlertMetricsAndSnapshot(t *testing.T) {
	var mu sync.Mutex
	var received []notify.Event
	webhook := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var event notify.Event
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			t.Errorf("decode alert: %v", err)
		}
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		response.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	artifactFactory, err := artifacts.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	telemetry, reader := newManualMetrics(t)
	acts := &Activities{
		artifactFactory: artifactFactory,
		redactor:        NewRedactor(),
		telemetry:       &Telemetry{metrics: telemetry},
		cfg:             Config{Hardening: HardeningConfig{AlertWebhookURL: webhook.URL}},
		log:             slog.New(slog.DiscardHandler),
	}

	sweep := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	out, err := acts.ReportConnectorHealth(context.Background(), ConnectorHealthInput{
		Connector:           "azdo",
		LastSuccessfulSweep: sweep,
		LastDelivery:        sweep.Add(-time.Minute),
		Subscription:        "disabled",
		SubscriptionDetail:  "the creator left the organization",
		QueueDepth:          map[string]int{"payments": 2},
	})
	if err != nil {
		t.Fatalf("ReportConnectorHealth: %v", err)
	}
	if out.Health.Status != forge.HealthDegraded || out.Health.Reason != "subscription_disabled" {
		t.Fatalf("health = %+v, want a degraded disabled subscription", out.Health)
	}
	if out.Alert == nil || !out.Alert.Delivered {
		t.Fatalf("health alert = %+v, want a delivered record", out.Alert)
	}
	if out.SnapshotError != "" {
		t.Fatalf("snapshot error: %s", out.SnapshotError)
	}

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("alerts delivered = %d, want 1", len(received))
	}
	event := received[0]
	mu.Unlock()
	if event.Kind != AlertConnectorDegraded || event.Severity != notify.SeverityCritical {
		t.Fatalf("alert = %+v", event)
	}
	for key, want := range map[string]string{
		"connector":    "azdo",
		"reason":       "subscription_disabled",
		"subscription": "disabled",
	} {
		if event.Detail[key] != want {
			t.Fatalf("alert detail[%q] = %q, want %q (detail %+v)", key, event.Detail[key], want, event.Detail)
		}
	}
	if !strings.Contains(event.Summary, "azdo") || !strings.Contains(event.Summary, "degraded") {
		t.Fatalf("alert summary does not name the connector and state: %q", event.Summary)
	}

	snapshot, err := forge.LoadHealthSnapshot(artifactFactory.Root())
	if err != nil {
		t.Fatalf("LoadHealthSnapshot: %v", err)
	}
	if len(snapshot.Connectors) != 1 || snapshot.Connectors[0].Connector != "azdo" {
		t.Fatalf("health snapshot = %+v", snapshot)
	}
	if snapshot.Connectors[0].Subscription != forge.SubscriptionDisabled {
		t.Fatalf("snapshot subscription = %q", snapshot.Connectors[0].Subscription)
	}

	collected := collectMetrics(t, reader)
	if got := gaugeValueFor(t, metricByName(t, collected, "factory_connector_last_successful_sweep_timestamp_seconds").Data, AttrConnector, "azdo"); got != sweep.Unix() {
		t.Fatalf("last successful sweep metric = %d, want %d", got, sweep.Unix())
	}
	if got := gaugeValueFor(t, metricByName(t, collected, "factory_scope_queue_depth").Data, AttrScope, "payments"); got != 2 {
		t.Fatalf("queue depth metric = %d, want 2", got)
	}
}
