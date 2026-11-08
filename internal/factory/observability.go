package factory

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"

	artifacts "github.com/mitkox/esf/internal/factoryartifacts"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/notify"
)

// Attribute keys used across factory spans.
//
// They are constants so a dashboard or query written today keeps working as the
// implementation changes. No attribute ever carries a credential.
const (
	AttrRunID              = "factory.run.id"
	AttrWorkflowID         = "factory.workflow.id"
	AttrWorkflowRun        = "factory.workflow.run_id"
	AttrRepository         = "repository.name"
	AttrRevision           = "repository.revision"
	AttrAgentHarness       = "agent.harness"
	AttrAgentModel         = "agent.model"
	AttrSandboxProvider    = "sandbox.provider"
	AttrSandboxTemplate    = "sandbox.template"
	AttrSandboxID          = "sandbox.id"
	AttrVerificationResult = "verification.result"
	AttrFactoryResult      = "factory.result"
	AttrAgentResult        = "agent.result"
	AttrCleanupResult      = "cleanup.result"
	AttrFactoryVersion     = "factory.version"
	AttrConnector          = "factory.connector"
	AttrScope              = "factory.scope"
	AttrFailureClass       = "factory.failure.class"
	AttrSubscriptionState  = "factory.subscription.state"
)

// Span names for the desired trace shape.
const (
	SpanRun             = "factory.run"
	SpanValidate        = "validate"
	SpanCubeCreate      = "cube.create"
	SpanRepoPrepare     = "repo.prepare"
	SpanNetworkLockdown = "sandbox.network.lockdown"
	SpanRecordBaseline  = "repo.baseline"
	SpanAgentRun        = "agent.run"
	SpanVerifyPrefix    = "verify."
	SpanArtifacts       = "artifacts.collect"
	SpanCubeDestroy     = "cube.destroy"
	SpanPrepareSandbox  = "sandbox.prepare"
)

// Telemetry bundles the tracer and meter providers.
//
// When no OTLP endpoint is configured the factory uses no-op providers. That
// keeps every instrumented code path identical whether or not a collector is
// running, so instrumentation cannot silently change behaviour between
// environments.
type Telemetry struct {
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	shutdown       []func(context.Context) error
	metrics        *Metrics
}

// Metrics holds the factory's instruments.
type Metrics struct {
	RunsTotal             metric.Int64Counter
	RunsSuccessTotal      metric.Int64Counter
	RunsFailedTotal       metric.Int64Counter
	RunDurationSeconds    metric.Float64Histogram
	SandboxCreateDuration metric.Float64Histogram
	AgentDurationSeconds  metric.Float64Histogram
	VerifyDurationSeconds metric.Float64Histogram
	SandboxLeaksTotal     metric.Int64Counter

	// Connector and publication health instruments. Timestamps are Unix
	// seconds and describe the last SUCCESSFUL observation, never an attempt.
	ConnectorLastSuccessfulSweep metric.Int64Gauge
	ConnectorLastDelivery        metric.Int64Gauge
	ConnectorSubscriptionState   metric.Int64Gauge
	PublicationFailuresTotal     metric.Int64Counter
	EvidenceFailuresTotal        metric.Int64Counter
	ScopeQueueDepth              metric.Int64Gauge
}

// NewTelemetry builds telemetry from configuration.
//
// It never fails the process for an observability problem: if the exporter
// cannot be constructed, the error is returned so the caller can decide, but
// the factory itself remains usable.
func NewTelemetry(ctx context.Context, cfg ObservabilityConfig) (*Telemetry, error) {
	if cfg.OTLPEndpoint == "" {
		return newNoopTelemetry(), nil
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	traceOptions := []otlptracegrpc.Option{otlptracegrpc.WithEndpointURL(cfg.OTLPEndpoint)}
	metricOptions := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpointURL(cfg.OTLPEndpoint)}
	endpoint, _ := url.Parse(cfg.OTLPEndpoint)
	if endpoint.Scheme == "http" {
		traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
		metricOptions = append(metricOptions, otlpmetricgrpc.WithInsecure())
	} else {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		if cfg.OTLPCAFile != "" {
			pem, err := os.ReadFile(cfg.OTLPCAFile)
			if err != nil {
				return nil, fmt.Errorf("read OTLP CA file: %w", err)
			}
			pool, err := x509.SystemCertPool()
			if err != nil {
				return nil, fmt.Errorf("load system CA roots: %w", err)
			}
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("OTLP CA file has no certificates")
			}
			tlsConfig.RootCAs = pool
		}
		traceOptions = append(traceOptions, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
		metricOptions = append(metricOptions, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "factory"
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(serviceName),
		attribute.String(AttrFactoryVersion, Version),
	))
	if err != nil {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceOptions...)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	metricExporter, err := otlpmetricgrpc.New(ctx, metricOptions...)
	if err != nil {
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithResource(res),
	)

	t := &Telemetry{
		tracerProvider: traceProvider,
		meterProvider:  meterProvider,
		shutdown: []func(context.Context) error{
			traceProvider.Shutdown,
			meterProvider.Shutdown,
		},
	}
	metrics, err := newMetrics(meterProvider)
	if err != nil {
		return nil, err
	}
	t.metrics = metrics
	return t, nil
}

func (cfg ObservabilityConfig) validate() error {
	if cfg.OTLPEndpoint == "" {
		if cfg.OTLPCAFile != "" {
			return fmt.Errorf("otel_ca_file requires otel_endpoint")
		}
		return nil
	}
	endpoint, err := url.Parse(cfg.OTLPEndpoint)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("otel_endpoint must be an http(s) URL with no path or credentials")
	}
	if endpoint.Scheme == "http" {
		host := endpoint.Hostname()
		if !strings.EqualFold(host, "localhost") && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
			return fmt.Errorf("remote otel_endpoint requires HTTPS")
		}
	}
	return nil
}

func newNoopTelemetry() *Telemetry {
	t := &Telemetry{
		tracerProvider: tracenoop.NewTracerProvider(),
		meterProvider:  metricnoop.NewMeterProvider(),
	}
	metrics, err := newMetrics(t.meterProvider)
	if err != nil {
		// The no-op meter cannot fail; if it somehow does, metrics are nil and
		// the recording helpers tolerate that.
		return t
	}
	t.metrics = metrics
	return t
}

// Tracer returns the factory's tracer.
func (t *Telemetry) Tracer() trace.Tracer {
	if t == nil || t.tracerProvider == nil {
		return tracenoop.NewTracerProvider().Tracer("factory")
	}
	return t.tracerProvider.Tracer("factory")
}

// Metrics returns the factory's instruments (never nil for a built Telemetry).
func (t *Telemetry) Metrics() *Metrics {
	if t == nil {
		return nil
	}
	return t.metrics
}

// Shutdown flushes telemetry. It is safe to call on a nil Telemetry.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var firstErr error
	for _, fn := range t.shutdown {
		if err := fn(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func newMetrics(provider metric.MeterProvider) (*Metrics, error) {
	meter := provider.Meter("factory")
	var err error
	m := &Metrics{}
	if m.RunsTotal, err = meter.Int64Counter("factory_runs_total",
		metric.WithDescription("Total factory runs started")); err != nil {
		return nil, err
	}
	if m.RunsSuccessTotal, err = meter.Int64Counter("factory_runs_success_total",
		metric.WithDescription("Factory runs that succeeded")); err != nil {
		return nil, err
	}
	if m.RunsFailedTotal, err = meter.Int64Counter("factory_runs_failed_total",
		metric.WithDescription("Factory runs that failed")); err != nil {
		return nil, err
	}
	if m.RunDurationSeconds, err = meter.Float64Histogram("factory_run_duration_seconds",
		metric.WithDescription("End-to-end factory run duration")); err != nil {
		return nil, err
	}
	if m.SandboxCreateDuration, err = meter.Float64Histogram("sandbox_create_duration_seconds",
		metric.WithDescription("Sandbox creation duration")); err != nil {
		return nil, err
	}
	if m.AgentDurationSeconds, err = meter.Float64Histogram("agent_duration_seconds",
		metric.WithDescription("Coding agent run duration")); err != nil {
		return nil, err
	}
	if m.VerifyDurationSeconds, err = meter.Float64Histogram("verification_duration_seconds",
		metric.WithDescription("Deterministic verification duration")); err != nil {
		return nil, err
	}
	if m.SandboxLeaksTotal, err = meter.Int64Counter("sandbox_leaks_total",
		metric.WithDescription("Sandboxes that outlived their run")); err != nil {
		return nil, err
	}
	if m.ConnectorLastSuccessfulSweep, err = meter.Int64Gauge("factory_connector_last_successful_sweep_timestamp_seconds",
		metric.WithDescription("Unix time of the last successful connector reconciliation sweep"),
		metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if m.ConnectorLastDelivery, err = meter.Int64Gauge("factory_connector_last_delivery_timestamp_seconds",
		metric.WithDescription("Unix time of the last authenticated connector delivery"),
		metric.WithUnit("s")); err != nil {
		return nil, err
	}
	if m.ConnectorSubscriptionState, err = meter.Int64Gauge("factory_connector_subscription_state",
		metric.WithDescription("One-hot forge event-subscription state (1 for the reported state, 0 otherwise)")); err != nil {
		return nil, err
	}
	if m.PublicationFailuresTotal, err = meter.Int64Counter("factory_publication_failures_total",
		metric.WithDescription("Publication attempts that failed, by failure class")); err != nil {
		return nil, err
	}
	if m.EvidenceFailuresTotal, err = meter.Int64Counter("factory_evidence_failures_total",
		metric.WithDescription("Evidence signing or verification failures, by failure class")); err != nil {
		return nil, err
	}
	if m.ScopeQueueDepth, err = meter.Int64Gauge("factory_scope_queue_depth",
		metric.WithDescription("Pending work items queued for one operator scope")); err != nil {
		return nil, err
	}
	return m, nil
}

// RecordRun records the terminal outcome of a run. Nil-safe.
func (m *Metrics) RecordRun(ctx context.Context, state RunState, duration time.Duration, attrs ...attribute.KeyValue) {
	if m == nil {
		return
	}
	opts := []metric.AddOption{metric.WithAttributes(attrs...)}
	m.RunsTotal.Add(ctx, 1, opts...)
	switch state {
	case StateSucceeded:
		m.RunsSuccessTotal.Add(ctx, 1, opts...)
	default:
		m.RunsFailedTotal.Add(ctx, 1, opts...)
	}
	m.RunDurationSeconds.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
}

// RecordConnectorSweep records the timestamp of a SUCCESSFUL reconciliation
// sweep. A failed sweep must not call this: an advanced timestamp would tell an
// operator reconciliation is working when it is not. Nil-safe.
func (m *Metrics) RecordConnectorSweep(ctx context.Context, connector string, at time.Time) {
	if m == nil || at.IsZero() || strings.TrimSpace(connector) == "" {
		return
	}
	m.ConnectorLastSuccessfulSweep.Record(ctx, at.UTC().Unix(),
		metric.WithAttributes(attribute.String(AttrConnector, connector)))
}

// RecordConnectorDelivery records the timestamp of an authenticated delivery.
// Nil-safe.
func (m *Metrics) RecordConnectorDelivery(ctx context.Context, connector string, at time.Time) {
	if m == nil || at.IsZero() || strings.TrimSpace(connector) == "" {
		return
	}
	m.ConnectorLastDelivery.Record(ctx, at.UTC().Unix(),
		metric.WithAttributes(attribute.String(AttrConnector, connector)))
}

// RecordSubscriptionState records the forge event-subscription state one-hot,
// so a dashboard can alert on the state without encoding it into the value.
// Nil-safe.
func (m *Metrics) RecordSubscriptionState(ctx context.Context, connector string, state forge.SubscriptionState) {
	if m == nil || strings.TrimSpace(connector) == "" {
		return
	}
	state = forge.ParseSubscriptionState(string(state))
	for _, candidate := range []forge.SubscriptionState{
		forge.SubscriptionActive, forge.SubscriptionDisabled,
		forge.SubscriptionProbationary, forge.SubscriptionUnknown,
	} {
		value := int64(0)
		if state == candidate {
			value = 1
		}
		m.ConnectorSubscriptionState.Record(ctx, value,
			metric.WithAttributes(
				attribute.String(AttrConnector, connector),
				attribute.String(AttrSubscriptionState, string(candidate)),
			))
	}
}

// RecordPublicationFailure counts one publication failure by class. Nil-safe.
func (m *Metrics) RecordPublicationFailure(ctx context.Context, class string) {
	if m == nil {
		return
	}
	m.PublicationFailuresTotal.Add(ctx, 1,
		metric.WithAttributes(attribute.String(AttrFailureClass, failureClass(class))))
}

// RecordEvidenceFailure counts one evidence signing or verification failure by
// class. Nil-safe.
func (m *Metrics) RecordEvidenceFailure(ctx context.Context, class string) {
	if m == nil {
		return
	}
	m.EvidenceFailuresTotal.Add(ctx, 1,
		metric.WithAttributes(attribute.String(AttrFailureClass, failureClass(class))))
}

// RecordScopeQueueDepth records the pending work-item count for one scope.
// Nil-safe.
func (m *Metrics) RecordScopeQueueDepth(ctx context.Context, scope string, depth int) {
	scope = strings.TrimSpace(scope)
	if m == nil || scope == "" {
		return
	}
	if depth < 0 {
		depth = 0
	}
	m.ScopeQueueDepth.Record(ctx, int64(depth),
		metric.WithAttributes(attribute.String(AttrScope, scope)))
}

func failureClass(class string) string {
	class = strings.TrimSpace(class)
	if class == "" {
		return "unknown"
	}
	return class
}

// ConnectorHealthInput is one connector liveness observation.
type ConnectorHealthInput struct {
	Connector           string         `json:"connector"`
	LastSuccessfulSweep time.Time      `json:"last_successful_sweep,omitempty"`
	LastDelivery        time.Time      `json:"last_delivery,omitempty"`
	Subscription        string         `json:"subscription,omitempty"`
	SubscriptionDetail  string         `json:"subscription_detail,omitempty"`
	QueueDepth          map[string]int `json:"queue_depth,omitempty"`
}

// ConnectorHealthOutput is the computed health and the alert it produced.
type ConnectorHealthOutput struct {
	Health forge.ConnectorHealth `json:"health"`
	// Alert is present when the observation was degraded. Delivery failure is
	// recorded in the record and never fails this activity.
	Alert *AlertRecord `json:"alert,omitempty"`
	// SnapshotError reports that the durable console snapshot could not be
	// written. Health observation itself still succeeded.
	SnapshotError string `json:"snapshot_error,omitempty"`
}

// ReportConnectorHealth computes liveness, records the health instruments,
// writes the durable console snapshot, and raises one alert when degraded.
//
// It is the hook a connector loop calls after each sweep/delivery observation.
// Alert delivery is best-effort; the durable record carries the failure.
func (a *Activities) ReportConnectorHealth(ctx context.Context, in ConnectorHealthInput) (ConnectorHealthOutput, error) {
	connector := strings.TrimSpace(in.Connector)
	if connector == "" {
		return ConnectorHealthOutput{}, fmt.Errorf("connector health: connector is required")
	}
	observation := forge.HealthObservation{
		Connector:           connector,
		LastSuccessfulSweep: in.LastSuccessfulSweep,
		LastDelivery:        in.LastDelivery,
		Subscription:        forge.ParseSubscriptionState(in.Subscription),
		SubscriptionDetail:  in.SubscriptionDetail,
		QueueDepth:          in.QueueDepth,
	}
	health := forge.EvaluateHealth(time.Now().UTC(), observation)

	if metrics := a.telemetry.Metrics(); metrics != nil {
		metrics.RecordConnectorSweep(ctx, connector, in.LastSuccessfulSweep)
		metrics.RecordConnectorDelivery(ctx, connector, in.LastDelivery)
		metrics.RecordSubscriptionState(ctx, connector, health.Subscription)
		for scope, depth := range in.QueueDepth {
			metrics.RecordScopeQueueDepth(ctx, scope, depth)
		}
	}

	out := ConnectorHealthOutput{Health: health}
	if health.Degraded() {
		severity := notify.SeverityWarning
		if health.Subscription == forge.SubscriptionDisabled || health.Subscription == forge.SubscriptionProbationary {
			severity = notify.SeverityCritical
		}
		record := a.raiseAlert(ctx, "", AlertConnectorDegraded, severity, healthAlertSummary(health), healthAlertDetail(health))
		out.Alert = &record
	}
	if a.artifactFactory != nil {
		if err := forge.SaveConnectorHealth(a.artifactFactory.Root(), health); err != nil {
			out.SnapshotError = err.Error()
			if a.redactor != nil {
				out.SnapshotError = a.redactor.Redact(out.SnapshotError)
			}
			if a.log != nil {
				a.log.WarnContext(ctx, "connector health snapshot not written",
					"connector", connector, "error", out.SnapshotError)
			}
		}
	}
	return out, nil
}

// ConnectorHealthReporter records connector liveness for the connector
// workload, which holds no sandbox provider, repository provider, or forge
// credential beyond the adapters it already owns.
type ConnectorHealthReporter struct {
	activities *Activities
}

// NewConnectorHealthReporter builds an observability-only activity set. Only
// ReportConnectorHealth is meaningful on it.
func NewConnectorHealthReporter(cfg Config, artifactFactory artifacts.Factory, telemetry *Telemetry, logger *slog.Logger) (*ConnectorHealthReporter, error) {
	if artifactFactory == nil {
		return nil, fmt.Errorf("connector health: artifact factory is required")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &ConnectorHealthReporter{activities: &Activities{
		artifactFactory: artifactFactory,
		telemetry:       telemetry,
		redactor:        NewRedactor(),
		cfg:             cfg,
		log:             logger,
	}}, nil
}

// Report records one observation, writes the durable console snapshot, and
// exports the documented connector instruments.
func (r *ConnectorHealthReporter) Report(ctx context.Context, in ConnectorHealthInput) (ConnectorHealthOutput, error) {
	if r == nil || r.activities == nil {
		return ConnectorHealthOutput{}, fmt.Errorf("connector health: reporter is not configured")
	}
	return r.activities.ReportConnectorHealth(ctx, in)
}

func healthAlertSummary(health forge.ConnectorHealth) string {
	return fmt.Sprintf("connector %s is %s: %s", health.Connector, health.Status, health.Detail)
}

func healthAlertDetail(health forge.ConnectorHealth) map[string]string {
	detail := map[string]string{
		"connector":    health.Connector,
		"status":       string(health.Status),
		"reason":       health.Reason,
		"subscription": string(health.Subscription),
	}
	if health.SubscriptionDetail != "" {
		detail["subscription_detail"] = health.SubscriptionDetail
	}
	if health.LastSuccessfulSweep != nil {
		detail["last_successful_sweep"] = health.LastSuccessfulSweep.Format(time.RFC3339)
	}
	if health.LastDelivery != nil {
		detail["last_delivery"] = health.LastDelivery.Format(time.RFC3339)
	}
	return detail
}
