package factory

import (
	"bytes"
	"fmt"
	"github.com/mitkox/esf/internal/secretfile"
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
	if !supportsManagedHarness(h.Type) {
		return sandbox.Network{}, false, fmt.Errorf("harness %q: cube_egress credential mode requires type unreal, opencode, pi, claude, or codex", name)
	}
	if err := validateCubeEgressConfig(name, h); err != nil {
		return sandbox.Network{}, false, err
	}
	secret, err := resolveHarnessCredential(h)
	if err != nil {
		return sandbox.Network{}, false, fmt.Errorf("harness %q: %w", name, err)
	}

	network, err := c.networkForHarnessCredential(name, h, secret)
	return network, true, err
}

func (c Config) networkForHarnessCredential(name string, h HarnessConfig, secret string) (sandbox.Network, error) {
	if err := validateCubeEgressConfig(name, h); err != nil {
		return sandbox.Network{}, err
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
			return sandbox.Network{}, fmt.Errorf("harness %q: invalid base_url port %q", name, port)
		}
		match.Port = parsed
	}

	header, format := "Authorization", "Bearer ${SECRET}"
	if strings.EqualFold(strings.TrimSpace(h.Type), "claude") {
		header, format = "x-api-key", "${SECRET}"
	}
	paths := []string{match.Path}
	switch strings.ToLower(strings.TrimSpace(h.Type)) {
	case "claude":
		base := strings.TrimRight(endpoint.Path, "/")
		paths = []string{base + "/v1/messages", base + "/v1/messages/count_tokens"}
	case "codex":
		base := strings.TrimRight(endpoint.Path, "/")
		paths = []string{base + "/responses", base + "/responses/compact"}
	}
	network := sandbox.Network{
		AllowInternet: boolPointer(false),
		AllowOut:      append([]string(nil), c.Sandbox.RuntimeAllowOut...),
	}
	for i, path := range paths {
		endpointMatch := match
		endpointMatch.Path = path
		ruleName := "factory-" + name + "-model"
		if i > 0 {
			ruleName += "-" + strconv.Itoa(i)
		}
		network.Rules = append(network.Rules, sandbox.NetworkRule{
			Name: ruleName, Match: endpointMatch,
			Action: sandbox.NetworkAction{
				Allow: true,
				Audit: "metadata",
				Inject: []sandbox.HeaderInjection{{
					Header: header,
					Secret: secret,
					Format: format,
				}},
			},
		})
	}
	return network, nil
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
	data, err := secretfile.Read(path, 2048)
	if err != nil {
		return "", fmt.Errorf("read credential file: %w", err)
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
