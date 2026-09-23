package factory

import (
	"context"
	"fmt"
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

	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	metricExporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlpmetricgrpc.WithInsecure(),
	)
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
