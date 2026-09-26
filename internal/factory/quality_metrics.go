package factory

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mitkox/esf/internal/assurance"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func (r *Runtime) observeQuality(ctx context.Context) {
	m := r.Telemetry.Metrics()
	if m == nil {
		return
	}
	runs, err := r.Quality.Store.List(ctx)
	if err != nil {
		return
	}
	counts := map[string]int64{}
	add := func(kind, status, risk string, n int64) { counts[kind+"/"+status+"/"+risk] += n }
	for _, run := range runs {
		risk := "unclassified"
		if run.Plan != nil {
			risk = run.Plan.Risk
		}
		status := "pending"
		if run.Decision != nil {
			status = run.Decision.Result
		}
		add("runs", status, risk, 1)
		for _, g := range run.Gates {
			add("gates", g.Status, risk, 1)
		}
		for _, nc := range run.NonConformances {
			add("nc", nc.Status, risk, 1)
		}
		add("exceptions", "granted", risk, int64(len(run.Exceptions)))
		add("missing", "required", risk, int64(len(assurance.MissingControls(run))))
		if run.Approval != nil {
			add("approval", run.Approval.Status, risk, 1)
		}
	}
	objects, err := r.Quality.Store.Objects(ctx, "capa")
	if err == nil {
		for _, body := range objects {
			var c assurance.CAPA
			if json.Unmarshal(body, &c) == nil {
				add("capa", c.Status, "unclassified", 1)
			}
		}
	}
	pending, err := r.Quality.Store.Pending(ctx)
	if err == nil {
		for _, o := range pending {
			add("outbox", o.Kind, "unclassified", 1)
		}
	}
	for key := range r.Quality.metricSeries {
		if _, ok := counts[key]; !ok {
			counts[key] = 0
		}
	}
	r.Quality.metricSeries = map[string]bool{}
	for key, count := range counts {
		parts := strings.Split(key, "/")
		m.QualityRecords.Record(ctx, count, metric.WithAttributes(attribute.String("kind", parts[0]), attribute.String("status", parts[1]), attribute.String("risk", parts[2])))
		r.Quality.metricSeries[key] = true
	}
}
