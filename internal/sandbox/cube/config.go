// Package cube implements the factory's sandbox provider on top of the
// CubeSandbox Go SDK (github.com/tencentcloud/CubeSandbox/sdk/go).
//
// It consumes an EXISTING CubeSandbox deployment. It never installs,
// reconfigures, resets, or removes Cube components, templates, or data, and it
// never mutates the egress policy of the deployment.
package cube

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/mitkox/esf/internal/tomlx"
)

// Defaults chosen to match the deployment discovered on this host, but every
// value remains overridable. Note that the SDK's own default API URL
// (http://127.0.0.1:3000) is deliberately NOT used as a fallback, because an
// unnoticed fallback to the wrong port produces a confusing failure. The API
// URL is required configuration.
const (
	DefaultProxyPortHTTP            = 80
	DefaultIdleTimeout              = 30 * time.Minute
	DefaultRequestTimeout           = 60 * time.Second
	DefaultCommandOutputBytes int64 = 4 << 20
	// DefaultTemplate is empty: operators must select an existing READY template.
	DefaultTemplate = ""
)

// Config configures the CubeSandbox provider.
//
// SECURITY: APIKey is a credential. It must never be logged, serialized into
// run manifests, or placed in a sandbox.
type Config struct {
	// APIURL is the Cube (E2B-compatible) control-plane endpoint.
	APIURL string `toml:"api_url"`
	// APIKey authenticates to the Cube API. Empty when the deployment has
	// authentication disabled (as on this host).
	APIKey string `toml:"api_key"`
	// TemplateID is an operator-approved, pre-existing sandbox template.
	TemplateID string `toml:"template_id"`
	// ProxyNodeIP is the CubeProxy node used for data-plane requests.
	ProxyNodeIP string `toml:"proxy_node_ip"`
	// ProxyPortHTTP is the CubeProxy listener port.
	ProxyPortHTTP int `toml:"proxy_port_http"`
	// ProxyScheme is "http" or "https"; empty lets the SDK infer from the port.
	ProxyScheme string `toml:"proxy_scheme"`
	// SandboxDomain is the wildcard domain used for per-sandbox hostnames.
	SandboxDomain string `toml:"sandbox_domain"`
	// IdleTimeout bounds a sandbox's lifetime without activity.
	IdleTimeout tomlx.Duration `toml:"idle_timeout"`
	// RequestTimeout bounds individual Cube API requests.
	RequestTimeout tomlx.Duration `toml:"request_timeout"`
	// CommandOutputBytes bounds stdout and stderr before the SDK accumulates it.
	CommandOutputBytes int64 `toml:"command_output_bytes"`
	// Version is an operator-recorded Cube release identifier, written into run
	// metadata when the API does not expose one. Never invented by the factory.
	Version string `toml:"version"`
}

// ConfigFromEnv builds a Config from FACTORY_CUBE_* and the SDK-compatible
// CUBE_* variables, then from defaults.
//
// Precedence (highest first): FACTORY_CUBE_<FIELD>, the bare Cube variable, the
// built-in default. CUBE_API_KEY is accepted but never logged.
func ConfigFromEnv() Config {
	cfg := Config{
		APIURL:         firstEnv("FACTORY_CUBE_API_URL", "CUBE_API_URL", "E2B_API_URL"),
		APIKey:         firstEnv("FACTORY_CUBE_API_KEY", "CUBE_API_KEY", "E2B_API_KEY"),
		TemplateID:     firstEnv("FACTORY_CUBE_TEMPLATE_ID", "CUBE_TEMPLATE_ID"),
		ProxyNodeIP:    firstEnv("FACTORY_CUBE_PROXY_NODE_IP", "CUBE_PROXY_NODE_IP"),
		ProxyScheme:    firstEnv("FACTORY_CUBE_PROXY_SCHEME", "CUBE_PROXY_SCHEME"),
		SandboxDomain:  firstEnv("FACTORY_CUBE_SANDBOX_DOMAIN", "CUBE_SANDBOX_DOMAIN"),
		ProxyPortHTTP:  intEnv(DefaultProxyPortHTTP, "FACTORY_CUBE_PROXY_PORT_HTTP", "CUBE_PROXY_PORT_HTTP"),
		IdleTimeout:    tomlx.FromStd(durationEnv(DefaultIdleTimeout, "FACTORY_CUBE_IDLE_TIMEOUT", "CUBE_TIMEOUT")),
		RequestTimeout: tomlx.FromStd(durationEnv(DefaultRequestTimeout, "FACTORY_CUBE_REQUEST_TIMEOUT", "CUBE_REQUEST_TIMEOUT")),
		Version:        firstEnv("FACTORY_CUBE_VERSION"),
	}
	if cfg.TemplateID == "" && cfg.APIURL != "" {
		cfg.TemplateID = DefaultTemplate
	}
	return cfg
}

// LoadConfigFile reads a TOML config file and overlays it on env-derived
// values, so an explicit environment variable still wins.
func LoadConfigFile(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read cube config %q: %w", path, err)
	}
	fileCfg := Config{}
	decoder := toml.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fileCfg); err != nil {
		return Config{}, fmt.Errorf("parse cube config %q: %w", path, err)
	}

	// Environment overrides anything the file did not set explicitly is not
	// expressible in TOML, so treat the file as authoritative for the fields it
	// sets and fall back to env/defaults for the rest.
	cfg := ConfigFromEnv()
	mergeNonZero(&cfg, fileCfg)
	return cfg, nil
}

// mergeNonZero copies non-zero fields from src onto dst.
func mergeNonZero(dst *Config, src Config) {
	if src.APIURL != "" {
		dst.APIURL = src.APIURL
	}
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
	if src.TemplateID != "" {
		dst.TemplateID = src.TemplateID
	}
	if src.ProxyNodeIP != "" {
		dst.ProxyNodeIP = src.ProxyNodeIP
	}
	if src.ProxyPortHTTP != 0 {
		dst.ProxyPortHTTP = src.ProxyPortHTTP
	}
	if src.ProxyScheme != "" {
		dst.ProxyScheme = src.ProxyScheme
	}
	if src.SandboxDomain != "" {
		dst.SandboxDomain = src.SandboxDomain
	}
	if src.IdleTimeout != 0 {
		dst.IdleTimeout = src.IdleTimeout
	}
	if src.RequestTimeout != 0 {
		dst.RequestTimeout = src.RequestTimeout
	}
	if src.Version != "" {
		dst.Version = src.Version
	}
}

// Validate rejects a configuration that cannot work, at startup rather than at
// the first sandbox creation.
func (c Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.APIURL) == "" {
		problems = append(problems, "api_url is required (set CUBE_API_URL; this deployment's API is not on the SDK default port)")
	} else if endpoint, err := url.Parse(c.APIURL); err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
		problems = append(problems, "api_url must be an http(s) URL without userinfo")
	} else if endpoint.Scheme == "http" && !isLoopback(endpoint.Hostname()) {
		problems = append(problems, "remote api_url requires HTTPS")
	}
	if strings.TrimSpace(c.TemplateID) == "" {
		problems = append(problems, "template_id is required (set CUBE_TEMPLATE_ID to an existing READY template)")
	}
	if c.ProxyPortHTTP < 0 || c.ProxyPortHTTP > 65535 {
		problems = append(problems, fmt.Sprintf("proxy_port_http %d is out of range", c.ProxyPortHTTP))
	}
	if c.ProxyScheme != "" && c.ProxyScheme != "http" && c.ProxyScheme != "https" {
		problems = append(problems, "proxy_scheme must be http or https")
	}
	if c.ProxyNodeIP != "" && !isLoopback(c.ProxyNodeIP) && c.ProxyScheme != "https" {
		problems = append(problems, "remote Cube data proxy requires proxy_scheme = https")
	}
	if c.IdleTimeout.Std() < 0 {
		problems = append(problems, "idle_timeout must not be negative")
	}
	if c.RequestTimeout.Std() < 0 {
		problems = append(problems, "request_timeout must not be negative")
	}
	if c.CommandOutputBytes != 0 && c.CommandOutputBytes < 2048 {
		problems = append(problems, "command_output_bytes must be at least 2048")
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid cubesandbox configuration: %s", strings.Join(problems, "; "))
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// String renders the configuration with the API key redacted, so it is safe to
// log at startup.
func (c Config) String() string {
	return fmt.Sprintf("cubesandbox{api_url=%s template=%s proxy_node=%s proxy_port=%d idle_timeout=%s request_timeout=%s api_key=%s}",
		c.APIURL, c.TemplateID, c.ProxyNodeIP, c.ProxyPortHTTP, c.IdleTimeout, c.RequestTimeout, redactPresence(c.APIKey))
}

func redactPresence(secret string) string {
	if secret == "" {
		return "unset"
	}
	return "set(redacted)"
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

func intEnv(fallback int, names ...string) int {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
				return parsed
			}
		}
	}
	return fallback
}

func durationEnv(fallback time.Duration, names ...string) time.Duration {
	for _, name := range names {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			continue
		}
		if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
			return parsed
		}
		// The SDK historically accepted a bare number of seconds.
		if seconds, err := strconv.ParseFloat(v, 64); err == nil && seconds > 0 {
			return time.Duration(seconds * float64(time.Second))
		}
	}
	return fallback
}

// ErrNotConfigured is returned when a required setting is missing.
var ErrNotConfigured = errors.New("cubesandbox provider is not configured")
