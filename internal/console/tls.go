package console

import (
	"crypto/tls"
	"github.com/mitkox/esf/internal/httpsecurity"
)

func loadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	return httpsecurity.LoadTLSConfig(certFile, keyFile)
}
