package factory

import (
	"context"
	"strings"
	"testing"
)

func TestTelemetryRequiresTLSForRemoteCollector(t *testing.T) {
	for _, endpoint := range []string{"collector.example:4317", "http://collector.example:4317", "http://127.0.0.1:4317/path"} {
		_, err := NewTelemetry(context.Background(), ObservabilityConfig{OTLPEndpoint: endpoint})
		if err == nil {
			t.Fatalf("accepted unsafe collector %q", endpoint)
		}
	}
	if err := (ObservabilityConfig{OTLPEndpoint: "https://collector.example:4317"}).validate(); err != nil {
		t.Fatalf("rejected TLS collector: %v", err)
	}
	if _, err := NewTelemetry(context.Background(), ObservabilityConfig{OTLPEndpoint: "https://collector.example:4317", OTLPCAFile: "/missing/esf-ca.pem"}); err == nil || !strings.Contains(err.Error(), "OTLP CA") {
		t.Fatalf("missing CA error = %v", err)
	}
}
