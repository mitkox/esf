package console

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// writeTestCertificate creates a self-signed certificate for 127.0.0.1 and
// localhost. It is test-only material written to a temporary directory.
func writeTestCertificate(t *testing.T, dir string, keyPermissions os.FileMode) (string, string) {
	t.Helper()
	return writeTestCertificateWithValidity(t, dir, keyPermissions, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
}

func writeTestCertificateWithValidity(t *testing.T, dir string, keyPermissions os.FileMode, notBefore, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "console-test"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPath := filepath.Join(dir, "console.crt")
	keyPath := filepath.Join(dir, "console.key")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, keyPermissions); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}

func TestLoadTLSConfigValidatesPairsAndKeyPermissions(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertificate(t, dir, 0o600)
	config, err := loadTLSConfig(certPath, keyPath)
	if err != nil {
		t.Fatalf("loadTLSConfig: %v", err)
	}
	if config == nil || config.MinVersion != tls.VersionTLS12 {
		t.Fatalf("unexpected TLS config: %+v", config)
	}
	if config != nil && len(config.Certificates) != 1 {
		t.Fatalf("expected one certificate, got %d", len(config.Certificates))
	}

	if _, err := loadTLSConfig("", ""); err != nil {
		t.Fatalf("empty TLS config should be allowed: %v", err)
	}
	if _, err := loadTLSConfig(certPath, ""); err == nil {
		t.Fatalf("expected an error for a certificate without a key")
	}
	if _, err := loadTLSConfig("", keyPath); err == nil {
		t.Fatalf("expected an error for a key without a certificate")
	}

	exposedKey := filepath.Join(dir, "exposed.key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if err := os.WriteFile(exposedKey, data, 0o644); err != nil {
		t.Fatalf("write exposed key: %v", err)
	}
	if err := os.Chmod(exposedKey, 0o644); err != nil {
		t.Fatalf("chmod exposed key: %v", err)
	}
	if _, err := loadTLSConfig(certPath, exposedKey); err == nil || !strings.Contains(err.Error(), "readable outside its owner") {
		t.Fatalf("expected a world-readable key rejection, got %v", err)
	}

	groupKey := filepath.Join(dir, "group.key")
	if err := os.WriteFile(groupKey, data, 0o640); err != nil {
		t.Fatalf("write group key: %v", err)
	}
	if err := os.Chmod(groupKey, 0o640); err != nil {
		t.Fatalf("chmod group key: %v", err)
	}
	if _, err := loadTLSConfig(certPath, groupKey); err == nil {
		t.Fatalf("expected a group-readable key rejection")
	}

	expiredCert, expiredKey := writeTestCertificateWithValidity(t, t.TempDir(), 0o600, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	if _, err := loadTLSConfig(expiredCert, expiredKey); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected an expired certificate rejection, got %v", err)
	}
}

func TestServeTLSProvidesTransportSecurityHeaders(t *testing.T) {
	root, clock := buildFixtureStore(t)
	dir := t.TempDir()
	certPath, keyPath := writeTestCertificate(t, dir, 0o600)
	server, err := New(Config{
		DataDir: root, ReadToken: testToken, Live: false, Now: clock.Now,
		TLSCertFile: certPath, TLSKeyFile: keyPath,
		Static: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addresses := make(chan net.Addr, 1)
	served := make(chan error, 1)
	go func() {
		served <- server.Serve(ctx, "127.0.0.1:0", func(address net.Addr) { addresses <- address })
	}()
	var address net.Addr
	select {
	case address = <-addresses:
	case err := <-served:
		t.Fatalf("serve failed before listen: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for the TLS listener")
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2: true,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				NextProtos:         []string{"h2", "http/1.1"},
				InsecureSkipVerify: true, // self-signed test certificate
			},
		},
	}
	request, err := http.NewRequest(http.MethodGet, "https://"+address.String()+"/api/v1/console/healthz", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("TLS request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("Strict-Transport-Security = %q", got)
	}
	if response.ProtoMajor != 2 {
		t.Fatalf("expected HTTP/2 negotiation, got %s", response.Proto)
	}

	// The server must refuse a TLS 1.1 handshake.
	legacyDialer := &net.Dialer{Timeout: 5 * time.Second}
	if conn, err := tls.DialWithDialer(legacyDialer, "tcp", address.String(), &tls.Config{
		MinVersion:         tls.VersionTLS10,
		MaxVersion:         tls.VersionTLS11,
		InsecureSkipVerify: true,
	}); err == nil {
		conn.Close()
		t.Fatalf("expected the TLS 1.1 handshake to be refused")
	}

	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve returned an error on shutdown: %v", err)
	}
}

func TestPlainHTTPDoesNotSendHSTS(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server, err := New(Config{
		DataDir: root, ReadToken: testToken, Live: false, Now: clock.Now,
		Static: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer server.Close()
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	status, _, header := request(t, httpServer, http.MethodGet, "/api/v1/console/session", testToken)
	if status != http.StatusOK {
		t.Fatalf("session status = %d", status)
	}
	if header.Get("Strict-Transport-Security") != "" {
		t.Fatalf("plain HTTP must not advertise HSTS")
	}
}

func TestAuthThrottleBlocksAfterRepeatedFailures(t *testing.T) {
	root, clock := buildFixtureStore(t)
	httpServer := newTestServer(t, root, clock, nil)

	for attempt := 0; attempt < authMaxFailures; attempt++ {
		if status, _, _ := request(t, httpServer, http.MethodGet, "/api/v1/console/overview", "wrong-token"); status != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt+1, status)
		}
	}
	status, body, header := request(t, httpServer, http.MethodGet, "/api/v1/console/overview", testToken)
	if status != http.StatusTooManyRequests {
		t.Fatalf("after repeated failures status = %d (%s), want 429", status, body)
	}
	if header.Get("Retry-After") == "" {
		t.Fatalf("rate-limited response must include Retry-After")
	}

	// The block is temporary: after the cooldown a valid token is accepted.
	clock.current = clock.current.Add(authBlockDuration + time.Second)
	if status, _, _ := request(t, httpServer, http.MethodGet, "/api/v1/console/overview", testToken); status != http.StatusOK {
		t.Fatalf("after cooldown status = %d, want 200", status)
	}
}

func TestClientKeyTrustsForwardedAddressOnlyFromLoopback(t *testing.T) {
	request := &http.Request{RemoteAddr: "127.0.0.1:12345", Header: http.Header{}}
	if got := clientKey(request); got != "127.0.0.1" {
		t.Fatalf("clientKey = %q", got)
	}
	request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	if got := clientKey(request); got != "203.0.113.9" {
		t.Fatalf("clientKey with trusted XFF = %q", got)
	}
	request.Header.Set("X-Forwarded-For", strings.Repeat("a", 200))
	if got := clientKey(request); len(got) != maxClientKeyLength {
		t.Fatalf("clientKey should be bounded, got %d characters", len(got))
	}

	// A direct/non-loopback client cannot rotate its throttle key by forging
	// the proxy header.
	direct := &http.Request{RemoteAddr: "198.51.100.7:4444", Header: http.Header{}}
	direct.Header.Set("X-Forwarded-For", "203.0.113.99")
	if got := clientKey(direct); got != "198.51.100.7" {
		t.Fatalf("non-loopback client key = %q, want the TCP peer", got)
	}
}

func TestForgedForwardedHeaderCannotBypassThrottle(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server, err := New(Config{
		DataDir: root, ReadToken: testToken, Live: false, Now: clock.Now,
		Static: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer server.Close()
	handler := server.Handler()

	attempt := func(token, forwarded string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/console/overview", nil)
		request.RemoteAddr = "198.51.100.7:4444" // non-loopback, so XFF is not trusted
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Forwarded-For", forwarded)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	for attemptIndex := 0; attemptIndex < authMaxFailures; attemptIndex++ {
		// Rotate the spoofed header; the TCP peer is the real key.
		if status := attempt("wrong-token", fmt.Sprintf("203.0.113.%d", attemptIndex)).Code; status != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", attemptIndex+1, status)
		}
	}
	if status := attempt(testToken, "203.0.113.250").Code; status != http.StatusTooManyRequests {
		t.Fatalf("forged XFF bypassed the throttle: status = %d, want 429", status)
	}
}

func TestAuthThrottleResetClearsOverflowBucket(t *testing.T) {
	clock := &testClock{current: time.Now()}
	throttle := newAuthThrottle(clock.Now)
	throttle.maxTracked = 1
	throttle.fail("tracked")
	throttle.fail("overflowed") // routed to the shared bucket
	throttle.mu.Lock()
	_, before := throttle.failures[authOverflowKey]
	throttle.mu.Unlock()
	if !before {
		t.Fatalf("expected the second client to use the overflow bucket")
	}
	throttle.reset("overflowed")
	throttle.mu.Lock()
	_, after := throttle.failures[authOverflowKey]
	throttle.mu.Unlock()
	if after {
		t.Fatalf("successful auth should clear the shared overflow bucket")
	}
}

func TestAuthThrottleTableIsBounded(t *testing.T) {
	clock := &testClock{current: time.Now()}
	throttle := newAuthThrottle(clock.Now)
	throttle.maxTracked = 2
	throttle.fail("first")
	throttle.fail("second")
	throttle.fail("third") // over capacity -> shared overflow bucket
	throttle.mu.Lock()
	tracked := len(throttle.failures)
	_, overflowed := throttle.failures[authOverflowKey]
	throttle.mu.Unlock()
	if tracked > throttle.maxTracked+1 {
		t.Fatalf("throttle table grew past its bound: %d entries", tracked)
	}
	if !overflowed {
		t.Fatalf("expected a shared overflow bucket")
	}
	// Rotating fresh keys cannot escape the shared bucket and then block it.
	for index := 0; index < authMaxFailures; index++ {
		throttle.fail(fmt.Sprintf("fresh-client-%d", index))
	}
	if allowed, _ := throttle.allow("another-fresh-client"); allowed {
		t.Fatalf("overflow bucket remained unblocked")
	}
}
