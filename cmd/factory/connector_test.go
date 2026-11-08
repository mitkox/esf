package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/forge/azuredevops"
)

type recordingIntakeSink struct {
	mu         sync.Mutex
	proposals  []forge.Proposal
	rejections []forge.Rejection
}

func (s *recordingIntakeSink) Propose(_ context.Context, proposal forge.Proposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proposals = append(s.proposals, proposal)
	return nil
}

func (s *recordingIntakeSink) Reject(_ context.Context, rejection forge.Rejection) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejections = append(s.rejections, rejection)
	return nil
}

func (s *recordingIntakeSink) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.proposals), len(s.rejections)
}

func TestRootCommandIncludesConnector(t *testing.T) {
	t.Parallel()

	command, _, err := rootCommand().Find([]string{"connector"})
	if err != nil {
		t.Fatalf("Find(connector): %v", err)
	}
	if command.Name() != "connector" || command.Short == "" {
		t.Fatalf("connector command = %#v", command)
	}
}

func TestConnectorCommandRequiresConfiguredConnector(t *testing.T) {
	t.Setenv("CUBE_API_URL", "http://127.0.0.1:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-test")

	path := filepath.Join(t.TempDir(), "factory.toml")
	if err := os.WriteFile(path, []byte("[hardening]\nacknowledge_open_egress = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newConnectorCommand(&path)
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no connectors are enabled") {
		t.Fatalf("Execute = %v, want a missing-connector error", err)
	}
}

func TestConnectorCommandRejectsInvalidRuntimeConfig(t *testing.T) {
	t.Setenv("CUBE_API_URL", "http://127.0.0.1:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-test")

	path := filepath.Join(t.TempDir(), "factory.toml")
	body := `
[hardening]
acknowledge_open_egress = true

[connectors.azdo]
kind = "azuredevops"
base_url = "https://dev.azure.com/acme"
token_file = "/run/secrets/azdo.token"
webhook_listen = "0.0.0.0:8091"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newConnectorCommand(&path)
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("Execute = %v, want a listener TLS error", err)
	}
}

// TestConnectorWebhookAuthenticatesAndBoundsOverListener exercises the receiver
// the command builds through a real HTTP listener. Authentication is checked
// before parsing, and the body bound is checked before any forge call.
func TestConnectorWebhookAuthenticatesAndBoundsOverListener(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "azdo.token")
	if err := os.WriteFile(tokenFile, []byte("esf:pat"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := factory.Default()
	cfg.Storage.DataDir = dir
	cfg.Connectors = map[string]factory.ConnectorConfig{
		"azdo": {
			Kind:          "azuredevops",
			BaseURL:       "http://127.0.0.1:1",
			TokenFile:     tokenFile,
			WebhookListen: "127.0.0.1:8091",
		},
	}
	sink := &recordingIntakeSink{}
	specs, err := buildConnectorRuntime(cfg, sink)
	if err != nil {
		t.Fatalf("buildConnectorRuntime: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	webhook, ok := specs[0].WebhookHandler.(*azuredevops.Webhook)
	if !ok {
		t.Fatalf("handler = %T, want *azuredevops.Webhook", specs[0].WebhookHandler)
	}
	server := httptest.NewServer(webhook)
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/hooks/azuredevops", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("esf", "wrong")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("unauthenticated POST: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", response.StatusCode)
	}

	oversized := bytes.Repeat([]byte("x"), 1<<20+1)
	request, err = http.NewRequest(http.MethodPost, server.URL+"/hooks/azuredevops", bytes.NewReader(oversized))
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("esf", "pat")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("oversized POST: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", response.StatusCode)
	}
	if stats := webhook.Stats(); stats.Unauthorized != 1 {
		t.Fatalf("stats = %#v, want one authentication rejection", stats)
	}
	if proposals, rejections := sink.counts(); proposals != 0 || rejections != 0 {
		t.Fatalf("proposals = %d, rejections = %d, want none", proposals, rejections)
	}
}
