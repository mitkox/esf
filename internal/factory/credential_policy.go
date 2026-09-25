package factory

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mitkox/esf/internal/sandbox"
)

const cubeEgressCredentialMode = "cube_egress"

// runtimeNetworkForHarness resolves an operator-owned credential only inside
// the activity worker. The value is sent directly to Cube's control plane and
// never becomes workflow input, sandbox environment, or durable evidence.
func (c Config) runtimeNetworkForHarness(name string) (sandbox.Network, bool, error) {
	h, ok := c.Harnesses[name]
	if !ok {
		// Tests and embedders may inject a registry independently of Config.
		// Such harnesses have no operator-declared egress policy to apply.
		return sandbox.Network{}, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(h.CredentialMode), cubeEgressCredentialMode) {
		return sandbox.Network{}, false, nil
	}
	if !strings.EqualFold(strings.TrimSpace(h.Type), "unreal") {
		return sandbox.Network{}, false, fmt.Errorf("harness %q: cube_egress credential mode requires type unreal", name)
	}
	if err := validateCubeEgressConfig(name, h); err != nil {
		return sandbox.Network{}, false, err
	}
	secret, err := resolveHarnessCredential(h)
	if err != nil {
		return sandbox.Network{}, false, fmt.Errorf("harness %q: %w", name, err)
	}

	endpoint, _ := url.Parse(strings.TrimSpace(h.BaseURL))
	host := endpoint.Hostname()
	match := sandbox.NetworkMatch{
		Scheme: "https",
		Host:   host,
		SNI:    host,
		Method: []string{"POST"},
		Path:   egressPath(endpoint.Path),
	}
	if port := endpoint.Port(); port != "" {
		parsed, parseErr := strconv.Atoi(port)
		if parseErr != nil || parsed < 1 || parsed > 65535 {
			return sandbox.Network{}, false, fmt.Errorf("harness %q: invalid base_url port %q", name, port)
		}
		match.Port = parsed
	}

	header, format := "Authorization", "Bearer ${SECRET}"
	return sandbox.Network{
		AllowInternet: boolPointer(false),
		AllowOut:      append([]string(nil), c.Sandbox.RuntimeAllowOut...),
		Rules: []sandbox.NetworkRule{{
			Name:  "factory-" + name + "-model",
			Match: match,
			Action: sandbox.NetworkAction{
				Allow: true,
				Audit: "metadata",
				Inject: []sandbox.HeaderInjection{{
					Header: header,
					Secret: secret,
					Format: format,
				}},
			},
		}},
	}, true, nil
}

func egressPath(basePath string) string {
	basePath = strings.TrimRight(strings.TrimSpace(basePath), "/")
	if basePath == "" {
		return "/*"
	}
	return basePath + "/*"
}

func validateCubeEgressConfig(name string, h HarnessConfig) error {
	if strings.EqualFold(strings.TrimSpace(h.Provider), "ollama") {
		return fmt.Errorf("harness %q: ollama does not use a provider credential", name)
	}
	endpoint, err := url.Parse(strings.TrimSpace(h.BaseURL))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("harness %q: cube_egress requires an https base_url without userinfo, query, or fragment", name)
	}
	if net.ParseIP(endpoint.Hostname()) != nil {
		return fmt.Errorf("harness %q: cube_egress base_url must use a DNS hostname so Host and TLS SNI can both be pinned", name)
	}
	if strings.Contains(endpoint.Path, "*") {
		return fmt.Errorf("harness %q: cube_egress base_url path must not contain a wildcard", name)
	}
	hasEnv := strings.TrimSpace(h.APIKeyEnv) != ""
	hasFile := strings.TrimSpace(h.APIKeyFile) != ""
	if hasEnv == hasFile {
		return fmt.Errorf("harness %q: cube_egress requires exactly one of api_key_env or api_key_file", name)
	}
	if hasFile && !filepath.IsAbs(strings.TrimSpace(h.APIKeyFile)) {
		return fmt.Errorf("harness %q: api_key_file must be an absolute path", name)
	}
	return nil
}

func resolveHarnessCredential(h HarnessConfig) (string, error) {
	if path := strings.TrimSpace(h.APIKeyFile); path != "" {
		return readCredentialFile(path)
	}
	name := strings.TrimSpace(h.APIKeyEnv)
	value := os.Getenv(name)
	if name == "" || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("credential environment variable %s is empty", name)
	}
	return value, nil
}

func readCredentialFile(path string) (string, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect credential path: %w", err)
	}
	if !pathInfo.Mode().IsRegular() {
		return "", fmt.Errorf("credential file must be a regular file, not a symlink or device")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open credential file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect credential file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("credential file must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("credential file permissions %04o expose it to group or other users", info.Mode().Perm())
	}
	data, err := io.ReadAll(io.LimitReader(file, 2049))
	if err != nil {
		return "", fmt.Errorf("read credential file: %w", err)
	}
	if len(data) > 2048 {
		return "", fmt.Errorf("credential file exceeds 2048 bytes")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return "", fmt.Errorf("credential file is empty")
	}
	return string(data), nil
}

func validateCredentialControlPlane(rawURL string) error {
	endpoint, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || endpoint.Hostname() == "" {
		return fmt.Errorf("cube.api_url is invalid for credential injection")
	}
	if endpoint.Scheme == "https" {
		return nil
	}
	host := endpoint.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("cube.api_url must use HTTPS or loopback when cube_egress carries credentials")
}

func boolPointer(value bool) *bool { return &value }
