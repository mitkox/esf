package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/turnexec"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunRefusesRemotePlaintextBeforeProviderAccess(t *testing.T) {
	oldFlags, oldArgs := flag.CommandLine, os.Args
	defer func() { flag.CommandLine = oldFlags; os.Args = oldArgs }()
	for _, address := range []string{"0.0.0.0:8091", "[::]:8091", "192.0.2.1:8091"} {
		flag.CommandLine = flag.NewFlagSet("turn-executor", flag.ContinueOnError)
		os.Args = []string{"turn-executor", "--listen", address}
		if err := run(); err == nil || !strings.Contains(err.Error(), "requires TLS") {
			t.Fatalf("%s: %v", address, err)
		}
	}
}

func TestNativeTLSAuthenticatesAndRejectsPlaintext(t *testing.T) {
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := fixture.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	fixture.Close()
	tokenPath := filepath.Join(t.TempDir(), "service.token")
	if err := os.WriteFile(tokenPath, []byte(strings.Repeat("a", 64)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := loadServiceToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[string]turnexec.Profile{"test": {Template: "approved", Command: []string{"agent"}, Harness: "test", TimeoutSeconds: 10}}
	service, err := turnexec.Open(context.Background(), t.TempDir(), token, profiles, sandbox.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addresses := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, "0.0.0.0:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, service, func(addr net.Addr) { addresses <- addr })
	}()
	var addr net.Addr
	select {
	case addr = <-addresses:
	case err := <-done:
		t.Fatalf("serve: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("listener timeout")
	}
	_, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		t.Fatal(err)
	}
	base := "127.0.0.1:" + port
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, auth := range []bool{false, true} {
		req, _ := http.NewRequest("GET", "https://"+base+"/v1/profiles/test", nil)
		if auth {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := http.StatusUnauthorized
		if auth {
			want = http.StatusOK
		}
		if resp.StatusCode != want {
			t.Fatalf("auth=%v status=%d want=%d", auth, resp.StatusCode, want)
		}
	}
	plain := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "http://"+base+"/v1/profiles/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := plain.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("plaintext reached API: %d", resp.StatusCode)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("shutdown timeout")
	}
}

func TestServiceTokenRejectsUnusableCredentials(t *testing.T) {
	for _, token := range []string{strings.Repeat("a", 32) + ",", strings.Repeat("a", 16) + "\u00a0" + strings.Repeat("b", 16)} {
		t.Setenv("ESF_TURN_SERVICE_TOKEN", token)
		if _, err := loadServiceToken(""); err == nil {
			t.Fatal("unusable service token accepted")
		}
	}
}
