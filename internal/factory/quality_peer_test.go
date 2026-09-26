//go:build linux

package factory

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitkox/esf/internal/assurance"
)

func TestQualitySocketUsesKernelIdentity(t *testing.T) {
	r, _, input := qualityRuntimeFixture(t)
	socket := filepath.Join(t.TempDir(), "q.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: r.qualityHandler(), ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		uid, err := qualityPeerUID(c)
		if err != nil {
			t.Error(err)
			return ctx
		}
		return context.WithValue(ctx, qualityPeerKey{}, uid)
	}}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	defer func() { server.Close(); <-done }()
	client := NewQualityClient(socket)
	var admitted assurance.Run
	if err = client.Call(context.Background(), "POST", "/v1/runs", input, &admitted); err != nil {
		t.Fatal(err)
	}
	if admitted.Requester.ID != fmt.Sprintf("uid:%d", os.Getuid()) {
		t.Fatalf("untrusted requester %+v", admitted.Requester)
	}
	forged := map[string]any{"run_id": "forged", "actor": "quality-authority"}
	if err = client.Call(context.Background(), "POST", "/v1/runs", forged, nil); err == nil {
		t.Fatal("accepted client actor")
	}
	req, err := http.NewRequest("GET", "http://quality/v1/runs/"+admitted.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-ESF-Actor", "uid:0")
	response, err := client.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	events, err := r.Quality.Store.Events(context.Background(), "service")
	if err != nil || len(events) == 0 {
		t.Fatal("missing denied-action audit", err)
	}
}
