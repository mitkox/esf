package azuredevops

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mitkox/esf/internal/sandbox"
	"github.com/mitkox/esf/internal/secretfile"
)

const maxCloneCredentialBytes = 4096

// CloneCredential declares how a sandbox git clone authenticates at the Cube
// egress boundary.
//
// SecretFile is an operator-owned private file read through secretfile when the
// rule is built. Its content is either "username:password" or a bare token; it
// is base64-encoded into the injected Authorization value and is never placed
// in the sandbox environment or evidence. The declared paths are the exact git
// HTTP endpoint paths the clone uses.
type CloneCredential struct {
	SecretFile string
	Host       string
	Paths      []string
}

// EgressRules returns one allow rule per declared clone path, each injecting the
// Basic Authorization header at the trusted egress proxy.
//
// The returned rules contain the injected credential and belong only in the
// Cube control-plane request. They must never be persisted as evidence or
// placed in the sandbox environment.
func (c CloneCredential) EgressRules() ([]sandbox.NetworkRule, error) {
	secretFile := strings.TrimSpace(c.SecretFile)
	if secretFile == "" || !filepath.IsAbs(secretFile) {
		return nil, fmt.Errorf("azuredevops: clone credential secret file must be an absolute path")
	}
	host, port, err := cloneHost(c.Host)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(c.Paths))
	for _, rawPath := range c.Paths {
		path := strings.TrimSpace(rawPath)
		if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t\r\n\x00") || len(path) > 2048 {
			return nil, fmt.Errorf("azuredevops: clone endpoint path %q must be an absolute path without whitespace", rawPath)
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("azuredevops: at least one clone endpoint path is required")
	}

	data, err := secretfile.Read(secretFile, maxCloneCredentialBytes)
	if err != nil {
		return nil, fmt.Errorf("azuredevops: clone credential: %w", err)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" || strings.ContainsAny(secret, " \t\r\n\x00") {
		return nil, fmt.Errorf("azuredevops: clone credential is empty or contains whitespace")
	}
	if !strings.Contains(secret, ":") {
		secret = ":" + secret
	}
	injected := base64.StdEncoding.EncodeToString([]byte(secret))

	rules := make([]sandbox.NetworkRule, 0, len(paths))
	for i, path := range paths {
		rules = append(rules, sandbox.NetworkRule{
			Name: "azuredevops-clone-" + strconv.Itoa(i+1),
			Match: sandbox.NetworkMatch{
				Scheme: "https",
				Host:   host,
				SNI:    host,
				Method: []string{"GET", "POST"},
				Path:   path,
				Port:   port,
			},
			Action: sandbox.NetworkAction{
				Allow: true,
				Audit: "metadata",
				Inject: []sandbox.HeaderInjection{{
					Header: "Authorization",
					Secret: injected,
					Format: "Basic ${SECRET}",
				}},
			},
		})
	}
	return rules, nil
}

// CloneEgressRules returns the clone network rules declared on this connector.
// The rules carry the injected credential and are not evidence.
func (c *Client) CloneEgressRules() ([]sandbox.NetworkRule, error) {
	return c.clone.EgressRules()
}

// CloneEgressRulesForRepository derives the exact clone endpoints for one
// operator-mapped Azure DevOps repository and returns the egress rules that
// inject the clone credential at the trusted boundary.
//
// The sandbox sees only the repository URL; the Authorization header is added
// by the Cube egress proxy. The returned rules contain the injected secret and
// must never be persisted as evidence or placed in the sandbox environment.
func (c *Client) CloneEgressRulesForRepository(repository string) ([]sandbox.NetworkRule, error) {
	project, repo, err := c.repositoryCoordinates(repository)
	if err != nil {
		return nil, err
	}
	basePath := strings.Trim(c.base.Path, "/")
	prefix := ""
	if basePath != "" {
		prefix = "/" + basePath
	}
	clone := c.clone
	clone.Host = c.base.Host
	if len(clone.Paths) == 0 {
		root := prefix + "/" + url.PathEscape(project) + "/_git/" + url.PathEscape(repo)
		clone.Paths = []string{
			root + "/info/refs",
			root + "/git-upload-pack",
		}
	}
	return clone.EgressRules()
}

func cloneHost(raw string) (host string, port int, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "/ \t\r\n\x00") {
		return "", 0, fmt.Errorf("azuredevops: clone host is required and must not contain a scheme or path")
	}
	parsed, err := url.Parse("https://" + raw)
	if err != nil || parsed.Hostname() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", 0, fmt.Errorf("azuredevops: clone host %q is not a valid host", raw)
	}
	host = parsed.Hostname()
	if net.ParseIP(host) != nil {
		return "", 0, fmt.Errorf("azuredevops: clone host must use a DNS hostname so Host and TLS SNI can both be pinned")
	}
	if rawPort := parsed.Port(); rawPort != "" {
		port, err = strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("azuredevops: clone host port is invalid")
		}
	}
	return host, port, nil
}
