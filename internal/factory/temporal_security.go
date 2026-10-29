package factory

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/mitkox/esf/internal/secretfile"
	"net"
	"os"

	"go.temporal.io/sdk/client"
)

func (c TemporalConfig) connectionOptions() (client.ConnectionOptions, error) {
	var options client.ConnectionOptions
	host, _, err := net.SplitHostPort(c.HostPort)
	if err != nil {
		return options, fmt.Errorf("temporal.host_port must be host:port: %w", err)
	}
	local := host == "localhost" || net.ParseIP(host).IsLoopback()
	if !c.TLS {
		if !local || c.APIKeyEnv != "" || c.CAFile != "" || c.CertFile != "" || c.KeyFile != "" || c.ServerName != "" {
			return options, fmt.Errorf("temporal.tls is required for remote endpoints or authentication")
		}
		return options, nil
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.ServerName}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return options, fmt.Errorf("read Temporal CA: %w", err)
		}
		config.RootCAs = x509.NewCertPool()
		if !config.RootCAs.AppendCertsFromPEM(pem) {
			return options, fmt.Errorf("Temporal CA file contains no certificates")
		}
	}
	if (c.CertFile == "") != (c.KeyFile == "") {
		return options, fmt.Errorf("temporal.cert_file and key_file must be configured together")
	}
	if c.CertFile != "" {
		certPEM, err := os.ReadFile(c.CertFile)
		if err != nil {
			return options, fmt.Errorf("read Temporal client certificate: %w", err)
		}
		keyPEM, err := secretfile.Read(c.KeyFile, 1<<20)
		if err != nil {
			return options, fmt.Errorf("read Temporal client key: %w", err)
		}
		cert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return options, fmt.Errorf("load Temporal client certificate: %w", err)
		}
		config.Certificates = []tls.Certificate{cert}
	}
	if c.APIKeyEnv != "" && os.Getenv(c.APIKeyEnv) == "" {
		return options, fmt.Errorf("Temporal API key environment variable %s is empty", c.APIKeyEnv)
	}
	options.TLS = config
	return options, nil
}
