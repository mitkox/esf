package httpsecurity

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mitkox/esf/internal/secretfile"
)

// LoadTLSConfig builds an API server's TLS configuration from operator-provided
// certificate files.
//
// TLS is opt-in at the application layer: the console is normally bound to
// loopback and fronted by a reverse proxy that terminates TLS. Passing a
// certificate and key lets the console terminate TLS itself, which is what a
// single-host production deployment or a tailnet-only service needs. When no
// files are supplied the console serves plain HTTP on loopback only (the
// existing no-auth safety rules still apply).
//
// The private key is required to be non-world-readable: a key any local user
// can read defeats the point of the token and the certificate.
func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("both --tls-cert-file and --tls-key-file are required to enable TLS")
	}

	certInfo, err := os.Stat(certFile)
	if err != nil {
		return nil, fmt.Errorf("TLS certificate file: %w", err)
	}
	if !certInfo.Mode().IsRegular() {
		return nil, errors.New("TLS certificate file must be a regular file")
	}
	if certInfo.Size() == 0 || certInfo.Size() > 1<<20 {
		return nil, errors.New("TLS certificate file is empty or unreasonably large")
	}
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("read TLS certificate: %w", err)
	}
	keyPEM, err := secretfile.Read(keyFile, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("TLS private key: %w", err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate and key: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse TLS certificate: %w", err)
	}
	now := time.Now()
	if now.After(leaf.NotAfter) {
		return nil, fmt.Errorf("TLS certificate expired on %s", leaf.NotAfter.UTC().Format(time.RFC3339))
	}
	if now.Before(leaf.NotBefore) {
		return nil, fmt.Errorf("TLS certificate is not valid before %s", leaf.NotBefore.UTC().Format(time.RFC3339))
	}

	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		// Mozilla's "intermediate" compatibility profile: no SSLv3/TLS1.0/1.1
		// and no deprecated cipher suites are negotiated.
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
	}, nil
}
