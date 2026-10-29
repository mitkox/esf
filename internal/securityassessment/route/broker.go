// Package route supplies the factory-owned endpoint boundary for a Cube assessor.
package route

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Config struct {
	ListenAddress      string
	Hostname           string
	AccessToken        string
	TargetURL          string
	TargetTrafficToken string
	UpstreamClient     *http.Client
	AllowedPaths       []string
	MaxRequests        int
	MaxDuration        time.Duration
	MaxResponseBytes   int64
}

type Stats struct {
	Requests   uint64
	Forwarded  uint64
	Denied     uint64
	Incomplete uint64
}

type Broker struct {
	server      *http.Server
	publicCA    []byte
	address     string
	authority   string
	hostname    string
	accessToken string
	target      *url.URL
	targetToken string
	client      *http.Client
	paths       map[string]bool
	maxRequests uint64
	maxResponse int64
	deadline    time.Time
	cancel      context.CancelFunc
	timer       *time.Timer
	requests    atomic.Uint64
	forwarded   atomic.Uint64
	denied      atomic.Uint64
	incomplete  atomic.Uint64
}

var hostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)
var correlationPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func Start(cfg Config) (*Broker, error) {
	if cfg.MaxRequests == 0 {
		cfg.MaxRequests = 100
	}
	if cfg.MaxDuration == 0 {
		cfg.MaxDuration = 15 * time.Minute
	}
	if cfg.MaxResponseBytes == 0 {
		cfg.MaxResponseBytes = 1 << 20
	}
	if cfg.MaxRequests < 1 || cfg.MaxRequests > 100 || cfg.MaxDuration <= 0 || cfg.MaxDuration > 15*time.Minute || cfg.MaxResponseBytes < 1 || cfg.MaxResponseBytes > 16<<20 {
		return nil, errors.New("invalid endpoint bounds")
	}
	cfg.Hostname = strings.ToLower(cfg.Hostname)
	if len(cfg.Hostname) > 253 || !hostnamePattern.MatchString(cfg.Hostname) {
		return nil, errors.New("an exact endpoint hostname is required")
	}
	if len(cfg.AccessToken) < 32 || len(cfg.AccessToken) > 128 || !correlationPattern.MatchString(cfg.AccessToken) {
		return nil, errors.New("a private per-attempt endpoint capability is required")
	}
	host, port, err := net.SplitHostPort(cfg.ListenAddress)
	if err != nil || net.ParseIP(host) == nil {
		return nil, errors.New("an explicit IP and port are required for the endpoint listener")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return nil, errors.New("invalid endpoint listener port")
	}
	target, err := url.Parse(cfg.TargetURL)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || (target.Path != "" && target.Path != "/") || target.RawPath != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" {
		return nil, errors.New("target must be one fixed HTTPS origin")
	}
	if len(cfg.TargetTrafficToken) < 16 || len(cfg.TargetTrafficToken) > 4096 || strings.ContainsAny(cfg.TargetTrafficToken, "\r\n") {
		return nil, errors.New("invalid private target preview credential")
	}
	if cfg.UpstreamClient == nil {
		return nil, errors.New("a trusted upstream client is required")
	}
	if transport, ok := cfg.UpstreamClient.Transport.(*http.Transport); ok && transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		return nil, errors.New("upstream certificate verification must remain enabled")
	}
	paths := make(map[string]bool, len(cfg.AllowedPaths))
	if len(cfg.AllowedPaths) == 0 || len(cfg.AllowedPaths) > 16 {
		return nil, errors.New("explicit endpoint paths are required")
	}
	for _, entry := range cfg.AllowedPaths {
		if !strings.HasPrefix(entry, "/") || path.Clean(entry) != entry || strings.ContainsAny(entry, "%?*#\\\r\n") || strings.HasPrefix(entry, "/sandbox/") {
			return nil, errors.New("endpoint paths must be exact application paths")
		}
		paths[entry] = true
	}
	certificate, publicCA, err := ephemeralCertificate(cfg.Hostname, cfg.MaxDuration)
	if err != nil {
		return nil, fmt.Errorf("create endpoint TLS identity: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return nil, err
	}
	_, actualPort, _ := net.SplitHostPort(listener.Addr().String())
	client := *cfg.UpstreamClient
	client.Jar = nil
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.MaxDuration)
	b := &Broker{publicCA: publicCA, authority: net.JoinHostPort(cfg.Hostname, actualPort), hostname: cfg.Hostname, accessToken: cfg.AccessToken,
		target: target, targetToken: cfg.TargetTrafficToken, client: &client, paths: paths,
		maxRequests: uint64(cfg.MaxRequests), maxResponse: cfg.MaxResponseBytes,
		deadline: time.Now().Add(cfg.MaxDuration), cancel: cancel}
	b.address = "https://" + b.authority
	b.server = &http.Server{Handler: http.HandlerFunc(b.serveHTTP), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 5 * time.Second,
		MaxHeaderBytes: 16 << 10, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if !strings.EqualFold(hello.ServerName, cfg.Hostname) {
				return nil, errors.New("unapproved endpoint TLS authority")
			}
			return nil, nil
		}}
	go func() { _ = b.server.Serve(tls.NewListener(listener, tlsConfig)) }()
	b.timer = time.AfterFunc(cfg.MaxDuration, func() { cancel(); _ = b.server.Close() })
	return b, nil
}

func (b *Broker) URL() string { return b.address }

func (b *Broker) PublicCA() []byte { return append([]byte(nil), b.publicCA...) }

func (b *Broker) Stats() Stats {
	return Stats{Requests: b.requests.Load(), Forwarded: b.forwarded.Load(), Denied: b.denied.Load(), Incomplete: b.incomplete.Load()}
}

func (b *Broker) Close(ctx context.Context) error {
	b.timer.Stop()
	b.cancel()
	if err := b.server.Shutdown(ctx); err != nil {
		return errors.Join(err, b.server.Close())
	}
	return nil
}

func (b *Broker) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if b.requests.Add(1) > b.maxRequests || time.Now().After(b.deadline) {
		b.reject(w, http.StatusTooManyRequests, "incomplete", "endpoint budget exhausted")
		return
	}
	if r.TLS == nil || !strings.EqualFold(r.TLS.ServerName, b.hostname) || (!strings.EqualFold(r.Host, b.hostname) && !strings.EqualFold(r.Host, b.authority)) ||
		!hmac.Equal([]byte(r.Header.Get("X-ESF-Endpoint-Token")), []byte(b.accessToken)) ||
		r.Method != http.MethodGet || r.URL.IsAbs() || r.URL.Host != "" || !strings.HasPrefix(r.RequestURI, "/") ||
		strings.ContainsAny(r.RequestURI, "%\\") || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" ||
		path.Clean(r.URL.Path) != r.URL.Path || !b.paths[r.URL.Path] || r.ContentLength > 0 || len(r.TransferEncoding) > 0 ||
		!correlationPattern.MatchString(r.Header.Get("X-Correlation-ID")) || len(r.Header.Get("Authorization")) > 4096 ||
		(r.Header.Get("X-Synthetic-User") != "" && r.Header.Get("X-Synthetic-User") != "a" && r.Header.Get("X-Synthetic-User") != "b") {
		b.reject(w, http.StatusForbidden, "denied", "endpoint scope denied")
		return
	}
	target := *b.target
	target.Path = r.URL.Path
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		b.reject(w, http.StatusBadGateway, "incomplete", "target request unavailable")
		return
	}
	for _, name := range []string{"Authorization", "X-Synthetic-User", "X-Correlation-ID"} {
		if value := r.Header.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("cube-traffic-access-token", b.targetToken)
	response, err := b.client.Do(req)
	if err != nil {
		b.reject(w, http.StatusBadGateway, "incomplete", "target response unavailable")
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode <= 399 {
		b.reject(w, http.StatusForbidden, "denied", "target redirect denied")
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, b.maxResponse+1))
	if err != nil || int64(len(body)) > b.maxResponse {
		b.reject(w, http.StatusBadGateway, "incomplete", "target response incomplete")
		return
	}
	body = bytes.ReplaceAll(body, []byte(b.targetToken), []byte("[REDACTED]"))
	if contentType := response.Header.Get("Content-Type"); len(contentType) <= 1024 {
		w.Header().Set("Content-Type", strings.ReplaceAll(contentType, b.targetToken, "[REDACTED]"))
	}
	if correlation := response.Header.Get("X-Correlation-ID"); correlationPattern.MatchString(correlation) && !strings.Contains(correlation, b.targetToken) {
		w.Header().Set("X-Correlation-ID", correlation)
	}
	w.Header().Set("X-ESF-Route-Result", "forwarded")
	b.forwarded.Add(1)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func (b *Broker) reject(w http.ResponseWriter, code int, outcome, message string) {
	if outcome == "denied" {
		b.denied.Add(1)
	} else {
		b.incomplete.Add(1)
	}
	w.Header().Set("X-ESF-Route-Result", outcome)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, message)
}

func ephemeralCertificate(hostname string, duration time.Duration) (tls.Certificate, []byte, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	caSerial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leafSerial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: caSerial, Subject: pkix.Name{CommonName: "ESF ephemeral assessment endpoint"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(duration + time.Minute), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	// Parse the issued CA so the leaf receives its generated subject key ID as
	// authority key ID. Python 3.13's strict OpenSSL verification requires it.
	ca, err = x509.ParseCertificate(caDER)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	leaf := &x509.Certificate{SerialNumber: leafSerial, Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(duration), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), nil
}
