package cube

import (
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/tomlx"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	valid := func() Config {
		return Config{
			APIURL:         "http://127.0.0.1:4000",
			TemplateID:     "tpl-abc",
			IdleTimeout:    tomlx.FromStd(30 * time.Minute),
			RequestTimeout: tomlx.FromStd(time.Minute),
		}
	}

	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		// The SDK default is port 3000, which is wrong on this deployment; the
		// factory requires the URL explicitly rather than guessing.
		{"missing api url", func(c *Config) { c.APIURL = "" }, "api_url"},
		{"bad scheme", func(c *Config) { c.APIURL = "127.0.0.1:4000" }, "http(s)"},
		{"remote API without TLS", func(c *Config) { c.APIURL = "http://cube.example:4000" }, "requires HTTPS"},
		{"remote data proxy without TLS", func(c *Config) { c.ProxyNodeIP = "192.0.2.10" }, "requires proxy_scheme = https"},
		{"missing template", func(c *Config) { c.TemplateID = "" }, "template_id"},
		{"negative idle timeout", func(c *Config) { c.IdleTimeout = tomlx.FromStd(-time.Second) }, "idle_timeout"},
		{"negative request timeout", func(c *Config) { c.RequestTimeout = tomlx.FromStd(-time.Second) }, "request_timeout"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestConfigStringRedactsAPIKey makes sure the startup log line cannot leak a
// credential.
func TestConfigStringRedactsAPIKey(t *testing.T) {
	t.Parallel()
	const secret = "super-secret-cube-token"
	cfg := Config{
		APIURL:     "http://127.0.0.1:4000",
		APIKey:     secret,
		TemplateID: "tpl-abc",
	}
	rendered := cfg.String()
	if strings.Contains(rendered, secret) {
		t.Fatalf("the API key leaked into the config string: %s", rendered)
	}
	if !strings.Contains(rendered, "set(redacted)") {
		t.Fatalf("expected the key to be reported as set: %s", rendered)
	}
}

func TestConfigStringReportsUnsetKey(t *testing.T) {
	t.Parallel()
	cfg := Config{APIURL: "http://127.0.0.1:4000", TemplateID: "tpl"}
	if !strings.Contains(cfg.String(), "api_key=unset") {
		t.Fatalf("expected api_key=unset, got %s", cfg.String())
	}
}

func TestConfigFromEnvPrefersFactoryPrefixedVariables(t *testing.T) {
	t.Setenv("CUBE_API_URL", "http://fallback:4000")
	t.Setenv("FACTORY_CUBE_API_URL", "http://preferred:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-fallback")
	t.Setenv("FACTORY_CUBE_TEMPLATE_ID", "tpl-preferred")

	cfg := ConfigFromEnv()
	if cfg.APIURL != "http://preferred:4000" {
		t.Fatalf("APIURL = %q, want the FACTORY_-prefixed value", cfg.APIURL)
	}
	if cfg.TemplateID != "tpl-preferred" {
		t.Fatalf("TemplateID = %q, want the FACTORY_-prefixed value", cfg.TemplateID)
	}
}

func TestConfigFromEnvAcceptsSDKCompatibleNames(t *testing.T) {
	t.Setenv("FACTORY_CUBE_API_URL", "")
	t.Setenv("CUBE_API_URL", "")
	t.Setenv("CUBE_TEMPLATE_ID", "")
	t.Setenv("FACTORY_CUBE_TEMPLATE_ID", "")
	t.Setenv("E2B_API_URL", "http://e2b-style:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-e2b")

	cfg := ConfigFromEnv()
	if cfg.APIURL != "http://e2b-style:4000" {
		t.Fatalf("APIURL = %q, want the E2B-compatible value", cfg.APIURL)
	}
}

func TestMergeNonZero(t *testing.T) {
	t.Parallel()
	base := Config{APIURL: "http://env:4000", TemplateID: "tpl-env", ProxyPortHTTP: 80}
	file := Config{TemplateID: "tpl-file"}
	mergeNonZero(&base, file)
	if base.TemplateID != "tpl-file" {
		t.Fatalf("TemplateID = %q, want the file value", base.TemplateID)
	}
	if base.APIURL != "http://env:4000" {
		t.Fatalf("APIURL = %q, should be unchanged", base.APIURL)
	}
}

func TestParentDir(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/workspace/.factory/prompt.txt": "/workspace/.factory",
		"/prompt.txt":                    "",
		"/":                              "",
		"":                               "",
	}
	for input, want := range cases {
		if got := parentDir(input); got != want {
			t.Fatalf("parentDir(%q) = %q, want %q", input, got, want)
		}
	}
}
