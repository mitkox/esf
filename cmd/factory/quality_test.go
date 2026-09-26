package main

import (
	"bytes"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitkox/esf/internal/assurance"
)

func TestQualityAttestationExportsExactStoredBytes(t *testing.T) {
	// Keep below macOS's Unix-socket path limit, including long test names.
	dir, err := os.MkdirTemp("", "esf-q-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "q.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	statement := []byte(`{"_type":"https://in-toto.io/Statement/v1","subject":[{"name":"changes.patch","digest":{"sha256":"abcd"}}],"predicateType":"test","predicate":{"signed":false}}`)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/runs/example/attestation" {
			t.Error(r.URL.Path)
		}
		w.Write(statement)
		w.Write([]byte("\n"))
	})}
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	defer func() { server.Close(); <-done }()
	root := rootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"quality", "--socket", socket, "attestation", "example"})
	if err = root.Execute(); err != nil {
		t.Fatal(err)
	}
	if assurance.Digest(output.Bytes()) != assurance.Digest(statement) {
		t.Fatalf("export changed attestation bytes: %s", output.String())
	}
}
