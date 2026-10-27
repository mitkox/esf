package route

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEphemeralChainCarriesStrictVerifierKeyIdentifiers(t *testing.T) {
	chain, _, err := ephemeralCertificate("strict.esf.invalid", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(chain.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(chain.Certificate[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(leaf.AuthorityKeyId) == 0 || !bytes.Equal(leaf.AuthorityKeyId, ca.SubjectKeyId) || !leaf.BasicConstraintsValid {
		t.Fatal("issued chain omits the identifiers required by strict OpenSSL verification")
	}
}

func startBroker(t *testing.T, handler http.Handler, change func(*Config)) (*Broker, *http.Client, *atomic.Int64) {
	t.Helper()
	hits := &atomic.Int64{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(upstream.Close)
	cfg := Config{ListenAddress: "127.0.0.1:0", Hostname: "assessment-fixture.esf.invalid", TargetURL: upstream.URL,
		AccessToken:        "test-only-token-00000000000000000000",
		TargetTrafficToken: "private-preview-token-fixture", UpstreamClient: upstream.Client(),
		AllowedPaths: []string{"/health", "/api/resources/a", "/redirect"}, MaxDuration: time.Minute}
	if change != nil {
		change(&cfg)
	}
	b, err := Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := b.Close(ctx); err != nil {
			t.Errorf("close broker: %v", err)
		}
	})
	parsed, _ := url.Parse(b.URL())
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(b.PublicCA()) {
		t.Fatal("missing public CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+parsed.Port())
		}}
	t.Cleanup(transport.CloseIdleConnections)
	return b, &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, hits
}

func call(t *testing.T, client *http.Client, method, address, host string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Correlation-ID", "fixture-request-1")
	req.Header.Set("X-ESF-Endpoint-Token", "test-only-token-00000000000000000000")
	req.Header.Set("Authorization", "Bearer synthetic-user-token")
	req.Header.Set("X-Synthetic-User", "a")
	req.Header.Set("cube-traffic-access-token", "attacker-selected-token")
	if host != "" {
		req.Host = host
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestBrokerForwardsOnlyApprovedEndpointAndWithholdsPreviewCredential(t *testing.T) {
	b, client, hits := startBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" || r.Header.Get("cube-traffic-access-token") != "private-preview-token-fixture" {
			t.Errorf("wrong upstream route or preview credential: path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-user-token" || r.Header.Get("X-Synthetic-User") != "a" {
			t.Error("synthetic identity did not reach target")
		}
		if r.Header.Get("X-ESF-Endpoint-Token") != "" {
			t.Error("endpoint capability leaked to target")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Correlation-ID", r.Header.Get("X-Correlation-ID"))
		w.Header().Set("Set-Cookie", "secret-cookie=private-preview-token-fixture")
		fmt.Fprint(w, `{"ready":true,"echo":"private-preview-token-fixture"}`)
	}), nil)
	resp, body := call(t, client, "GET", b.URL()+"/health", "")
	if resp.StatusCode != 200 || resp.Header.Get("X-ESF-Route-Result") != "forwarded" || hits.Load() != 1 {
		t.Fatalf("approved control failed: status=%d hits=%d", resp.StatusCode, hits.Load())
	}
	if resp.Header.Get("Set-Cookie") != "" || strings.Contains(string(body), "private-preview-token-fixture") {
		t.Fatal("preview credential escaped upstream boundary")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil || payload["ready"] != true {
		t.Fatalf("approved JSON corrupted: %s", body)
	}
	if resp.Header.Get("X-Correlation-ID") != "fixture-request-1" {
		t.Fatal("correlation identity lost")
	}
	if got := b.Stats(); got.Requests != 1 || got.Forwarded != 1 || got.Denied != 0 || got.Incomplete != 0 {
		t.Fatalf("unexpected metadata counters: %+v", got)
	}
}

func TestBrokerRequiresIndependentEndpointCapability(t *testing.T) {
	b, client, hits := startBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "target") }), nil)
	for _, capability := range []string{"", "fedcba9876543210fedcba9876543210"} {
		req, err := http.NewRequest("GET", b.URL()+"/health", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Correlation-ID", "fixture-capability")
		req.Header.Set("Authorization", "Bearer synthetic-user-token")
		if capability != "" {
			req.Header.Set("X-ESF-Endpoint-Token", capability)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 || response.Header.Get("X-ESF-Route-Result") != "denied" {
			t.Fatal("missing or incorrect endpoint capability accepted")
		}
	}
	if hits.Load() != 0 {
		t.Fatal("unauthenticated request reached target")
	}
	response, _ := call(t, client, "GET", b.URL()+"/health", "")
	if response.StatusCode != 200 || hits.Load() != 1 {
		t.Fatal("valid independent capability control failed")
	}
}

func TestBrokerRejectsAuthorityPathMethodAndQueryEscapes(t *testing.T) {
	b, client, hits := startBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "target") }), nil)
	for _, tc := range []struct{ method, path, host string }{
		{"GET", "/health", "other-target.esf.invalid"},
		{"GET", "/health", "assessment-fixture.esf.invalid:18081"},
		{"GET", "/sandbox/other/49983/health", ""},
		{"GET", "/%68ealth", ""},
		{"GET", "/api/../health", ""},
		{"GET", "//health", ""},
		{"GET", "/health?next=/sandbox/other/49983", ""},
		{"GET", "/health?", ""},
		{"POST", "/health", ""},
		{"CONNECT", "/health", ""},
	} {
		resp, _ := call(t, client, tc.method, b.URL()+tc.path, tc.host)
		if resp.StatusCode != 403 || resp.Header.Get("X-ESF-Route-Result") != "denied" {
			t.Fatalf("escape accepted: %+v status=%d", tc, resp.StatusCode)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("rejected request reached target: %d", hits.Load())
	}
}

func TestBrokerRejectsForgedSNIWithoutDisablingClientVerification(t *testing.T) {
	b, client, hits := startBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "target") }), nil)
	parsed, _ := url.Parse(b.URL())
	_, err := client.Get("https://other-target.esf.invalid:" + parsed.Port() + "/health")
	if err == nil {
		t.Fatal("forged SNI handshake succeeded")
	}
	if hits.Load() != 0 {
		t.Fatal("forged TLS authority reached target")
	}
}

func TestBrokerDoesNotFollowOrExposeUpstreamRedirect(t *testing.T) {
	var escaped atomic.Int64
	escape := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { escaped.Add(1) }))
	defer escape.Close()
	b, client, hits := startBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", escape.URL+"/management")
		w.WriteHeader(http.StatusFound)
	}), nil)
	resp, body := call(t, client, "GET", b.URL()+"/redirect", "")
	if resp.StatusCode != 403 || resp.Header.Get("Location") != "" || escaped.Load() != 0 || hits.Load() != 1 {
		t.Fatalf("redirect escaped: status=%d escaped=%d", resp.StatusCode, escaped.Load())
	}
	if strings.Contains(string(body), escape.URL) {
		t.Fatal("out-of-scope redirect leaked through broker")
	}
}

func TestBrokerLimitsRequestsAndResponseBytesAsIncomplete(t *testing.T) {
	b, client, hits := startBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", 129)) }), func(cfg *Config) {
		cfg.MaxRequests = 1
		cfg.MaxResponseBytes = 128
	})
	first, _ := call(t, client, "GET", b.URL()+"/health", "")
	if first.StatusCode != 502 || first.Header.Get("X-ESF-Route-Result") != "incomplete" {
		t.Fatal("oversized response was not incomplete")
	}
	second, _ := call(t, client, "GET", b.URL()+"/health", "")
	if second.StatusCode != 429 || second.Header.Get("X-ESF-Route-Result") != "incomplete" || hits.Load() != 1 {
		t.Fatalf("request cap failed: status=%d upstream=%d", second.StatusCode, hits.Load())
	}
}

func TestBrokerRejectsAbsoluteFormRequestURI(t *testing.T) {
	b, _, hits := startBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "target") }), nil)
	parsed, _ := url.Parse(b.URL())
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(b.PublicCA())
	conn, err := tls.Dial("tcp", "127.0.0.1:"+parsed.Port(), &tls.Config{RootCAs: pool, ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	fmt.Fprintf(conn, "GET https://other-target.esf.invalid/health HTTP/1.1\r\nHost: %s\r\nX-Correlation-ID: fixture-request-1\r\nX-ESF-Endpoint-Token: test-only-token-00000000000000000000\r\nConnection: close\r\n\r\n", parsed.Host)
	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), "403 Forbidden") || hits.Load() != 0 {
		t.Fatalf("absolute-form authority accepted: %s", response)
	}
}

func TestBrokerRejectsUnsafeConfigurationBeforeBinding(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Hostname = "bad/host" },
		func(c *Config) { c.TargetURL = "http://target.example" },
		func(c *Config) { c.TargetURL = "https://target.example/sandbox/other" },
		func(c *Config) { c.AllowedPaths = []string{"/*"} },
		func(c *Config) { c.MaxRequests = 101 },
		func(c *Config) { c.TargetTrafficToken = "private\r\nInjected: invalid" },
		func(c *Config) { c.AccessToken = "short" },
	} {
		cfg := Config{ListenAddress: "127.0.0.1:0", Hostname: "assessment-fixture.esf.invalid", TargetURL: upstream.URL,
			AccessToken:        "test-only-token-00000000000000000000",
			TargetTrafficToken: "private-preview-token-fixture", UpstreamClient: upstream.Client(), AllowedPaths: []string{"/health"}}
		mutate(&cfg)
		if b, err := Start(cfg); err == nil {
			b.Close(context.Background())
			t.Fatal("unsafe broker configuration accepted")
		}
	}
}
