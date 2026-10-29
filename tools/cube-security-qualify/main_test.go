package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox/cube"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

func TestCleanupContinuesAfterOneDeletionFails(t *testing.T) {
	owner := "qualification-0123456789abcdef0123456789abcdef"
	deletedSecond := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == "GET" && req.URL.Path == "/sandboxes":
			items := []cubesandbox.SandboxInfo{{SandboxID: "a", Metadata: map[string]string{"esf.security.owner": owner}}, {SandboxID: "unrelated"}}
			if !deletedSecond {
				items = append(items, cubesandbox.SandboxInfo{SandboxID: "b", Metadata: map[string]string{"esf.security.owner": owner}})
			}
			json.NewEncoder(w).Encode(items)
		case req.Method == "DELETE" && req.URL.Path == "/sandboxes/a":
			w.WriteHeader(503)
		case req.Method == "DELETE" && req.URL.Path == "/sandboxes/b":
			deletedSecond = true
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected or unrelated resource request: %s %s", req.Method, req.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	client := cubesandbox.NewClient(cubesandbox.Config{APIURL: server.URL})
	defer client.Close()
	r := &report{Owner: owner}
	if reconcile(context.Background(), client, cube.Config{APIURL: server.URL}, r) == nil {
		t.Fatal("cleanup failure hidden")
	}
	if !deletedSecond {
		t.Fatal("first deletion failure skipped second VM")
	}
	if len(r.Remaining) != 1 || r.Remaining[0] != "a" {
		t.Fatalf("remaining owned IDs missing: %v", r.Remaining)
	}
}

func TestUnknownAllocationCannotClaimCleanAfterEmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { json.NewEncoder(w).Encode([]cubesandbox.SandboxInfo{}) }))
	defer server.Close()
	client := cubesandbox.NewClient(cubesandbox.Config{APIURL: server.URL})
	defer client.Close()
	r := &report{Owner: "qualification-0123456789abcdef0123456789abcdef", PendingAllocation: true}
	if reconcile(context.Background(), client, cube.Config{APIURL: server.URL}, r) == nil {
		t.Fatal("unknown acknowledgement became cleanup success")
	}
	for _, c := range r.Checks {
		if c.Name == "cleanup" && c.Status == "pass" {
			t.Fatal("empty snapshot accepted while allocation may complete later")
		}
	}
}

func TestCARejectsKeysAndMalformedInputs(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not a certificate"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("secret")})} {
		if validateCA(data) == nil {
			t.Fatal("accepted private or malformed CA input")
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture CA"}, IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCA(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionNeverAdmitsIncompleteQualification(t *testing.T) {
	for _, checks := range [][]check{nil, {{Name: "route", Status: "pass"}}, {{Name: "forbidden-port", Status: "fail"}}, {{Name: "tls", Status: "incomplete"}}} {
		r := report{Checks: checks}
		r.decide()
		if r.IntegrationEnabled || (r.Decision != "no-go" && r.Decision != "incomplete") {
			t.Fatalf("route-only evidence admitted integration: %+v", r)
		}
	}
}

func TestOwnedResourcesDoNotIncludeUnrelatedSandboxes(t *testing.T) {
	owner := "qualification-0123456789abcdef0123456789abcdef"
	infos := []cubesandbox.SandboxInfo{
		{SandboxID: "ours", Metadata: map[string]string{"esf.security.owner": owner}},
		{SandboxID: "other", Metadata: map[string]string{"esf.security.owner": owner + "x"}},
		{SandboxID: "none"},
	}
	got := ownedIDs(infos, owner)
	if len(got) != 1 || got[0] != "ours" {
		t.Fatalf("cleanup selected unrelated resources: %v", got)
	}
	if len(ownedIDs(infos, "")) != 0 {
		t.Fatal("empty owner must never select resources")
	}
}

func TestReportRefusesOverwriteAndContainsNoSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	r, err := newReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newReport(path); err == nil {
		t.Fatal("overwrote existing receipt")
	}
	r.Checks = append(r.Checks, check{Name: "positive-control", Status: "pass"})
	if err := r.save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Owner == "" || decoded.Owner != r.Owner {
		t.Fatal("lost retry reconciliation identity")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private receipt permissions: %v", info.Mode())
	}
	for _, key := range []string{"trafficAccessToken", "envdAccessToken", "api_key", "Authorization"} {
		if strings.Contains(string(data), key) {
			t.Fatalf("credential field serialized: %s", key)
		}
	}
}
