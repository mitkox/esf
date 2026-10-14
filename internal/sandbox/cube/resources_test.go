package cube

import (
	"context"
	"fmt"
	"github.com/mitkox/esf/internal/sandbox"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateActualTemplateResources(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"exact", `"cpuCount":2,"memoryMB":4096`, true},
		{"fractional", `"cpuCount":2,"cpuMilli":500,"memoryMB":4096`, false},
		{"wrong_memory", `"cpuCount":2,"memoryMB":2048`, false},
		{"missing", `"state":"running"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"sandboxID":"owned",%s}`, tc.body)
			}))
			defer server.Close()
			p, err := New(Config{APIURL: server.URL, TemplateID: "approved", ProxyNodeIP: "127.0.0.1", ProxyPortHTTP: 80})
			if err != nil {
				t.Fatal(err)
			}
			err = p.ValidateResources(context.Background(), "owned", sandbox.Limits{CPUMilli: 2000, MemoryMiB: 4096})
			if (err == nil) != tc.ok {
				t.Fatalf("resource verification=%v, want success=%v", err, tc.ok)
			}
		})
	}
}
