package cube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mitkox/esf/internal/sandbox"
)

func TestCreateExplicitDenialMatchesCube072APISchema(t *testing.T) {
	deny, allow := false, true
	for _, tc := range []struct {
		name, version string
		internet      *bool
		legacyDeny    bool
	}{
		{"stable denial", "0.7.2", &deny, true},
		{"tagged denial", "v0.7.2", &deny, true},
		{"default unchanged", "0.7.2", nil, false},
		{"allow unchanged", "0.7.2", &allow, false},
		{"future schema", "0.7.3", &deny, false},
		{"unknown schema", "", &deny, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests <- payload
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"sandboxID":"test-sandbox","templateID":"test-template"}`))
			}))
			defer server.Close()
			p, err := New(Config{APIURL: server.URL, TemplateID: "test-template", ProxyNodeIP: "127.0.0.1", ProxyPortHTTP: 80, Version: tc.version})
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if _, err = p.Create(context.Background(), sandbox.Spec{Network: sandbox.Network{AllowInternet: tc.internet}}); err != nil {
				t.Fatal(err)
			}
			payload := <-requests
			legacy, present := payload["allow_internet_access"]
			if present != tc.legacyDeny || (present && legacy != false) {
				t.Fatalf("CubeAPI 0.7.2 would read allow_internet_access=%v, present=%v; want denial=%v", legacy, present, tc.legacyDeny)
			}
			if tc.internet != nil && !*tc.internet && payload["allowInternetAccess"] != false {
				t.Fatal("canonical SDK denial was lost")
			}
		})
	}
}
