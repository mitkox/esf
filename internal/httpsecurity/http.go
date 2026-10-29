// Package httpsecurity enforces the transport and credential rules shared by ESF APIs.
package httpsecurity

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Loopback reports whether address is an explicit loopback host:port.
// Hostnames other than localhost are never trusted based on DNS resolution.
func Loopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateListen refuses transmission of API credentials over remote plaintext.
func ValidateListen(address string, tlsEnabled bool) error {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("listen address must be host:port: %w", err)
	}
	if !tlsEnabled && !Loopback(address) {
		return fmt.Errorf("non-loopback listener requires TLS; use a loopback listener behind a TLS reverse proxy")
	}
	return nil
}

// Bearer accepts exactly one Authorization header with the Bearer scheme.
func Bearer(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	fields := strings.Fields(values[0])
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || !ValidBearerToken(fields[1]) {
		return "", false
	}
	return fields[1], true
}

// Authenticated fails closed when the expected credential is missing.
func Authenticated(r *http.Request, expected string) bool {
	token, ok := Bearer(r)
	return ok && expected != "" && subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

// ValidBearerToken checks the ASCII bearer credential grammar. Configuration
// must enforce the same grammar as requests so accepted secrets remain usable.
func ValidBearerToken(token string) bool {
	value := strings.TrimRight(token, "=")
	if value == "" {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", c) {
			continue
		}
		return false
	}
	return true
}
