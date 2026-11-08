package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/factory"
)

func writeDoctorToken(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeDoctorSigningKey(t *testing.T, mode os.FileMode) string {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	body := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "evidence-signing-key.pem")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func doctorAPIConfig(tokenFile string) factory.Config {
	return factory.Config{API: factory.APIConfig{Enabled: true, Listen: "127.0.0.1:8090", TokenFile: tokenFile}}
}

func TestProductionAPIListenerCheck(t *testing.T) {
	t.Parallel()

	goodToken := writeDoctorToken(t, 0o600)
	groupReadable := writeDoctorToken(t, 0o640)

	cases := []struct {
		name   string
		config factory.Config
		status string
		want   string
	}{
		{"disabled", factory.Config{}, "skipped", "api.enabled is false"},
		{"valid", doctorAPIConfig(goodToken), "ok", ""},
		{"group readable token", doctorAPIConfig(groupReadable), "fail", "permissions"},
		{"missing token", doctorAPIConfig(filepath.Join(t.TempDir(), "missing")), "fail", "no such file"},
		{"plaintext remote listener", func() factory.Config {
			cfg := doctorAPIConfig(goodToken)
			cfg.API.Listen = "0.0.0.0:8090"
			return cfg
		}(), "fail", "non-loopback"},
		{"short token", func() factory.Config {
			path := filepath.Join(t.TempDir(), "short")
			if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return doctorAPIConfig(path)
		}(), "fail", "at least 32"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := productionAPIListener(tc.config)
			if check.Status != tc.status {
				t.Fatalf("status = %q, want %q (%s)", check.Status, tc.status, check.Detail)
			}
			if tc.want != "" && !strings.Contains(check.Detail, tc.want) {
				t.Fatalf("detail = %q, want %q", check.Detail, tc.want)
			}
		})
	}
}

func TestProductionConnectorConfigurationCheck(t *testing.T) {
	t.Parallel()

	goodToken := writeDoctorToken(t, 0o600)
	groupReadable := writeDoctorToken(t, 0o644)
	connector := func(tokenFile string) factory.ConnectorConfig {
		return factory.ConnectorConfig{
			Kind:      "azuredevops",
			BaseURL:   "https://dev.azure.com/acme",
			TokenFile: tokenFile,
		}
	}

	cases := []struct {
		name       string
		connectors map[string]factory.ConnectorConfig
		status     string
		want       string
	}{
		{"none", nil, "skipped", "no connectors configured"},
		{"valid", map[string]factory.ConnectorConfig{"azdo": connector(goodToken)}, "ok", ""},
		{"group readable token", map[string]factory.ConnectorConfig{"azdo": connector(groupReadable)}, "fail", "token_file"},
		{"unsupported kind", map[string]factory.ConnectorConfig{"azdo": {Kind: "jenkins", BaseURL: "https://dev.azure.com/acme", TokenFile: goodToken}}, "fail", "not supported"},
		{"plaintext remote webhook", map[string]factory.ConnectorConfig{"azdo": func() factory.ConnectorConfig {
			c := connector(goodToken)
			c.WebhookListen = "0.0.0.0:8091"
			return c
		}()}, "fail", "non-loopback"},
		{"missing webhook TLS files", map[string]factory.ConnectorConfig{"azdo": func() factory.ConnectorConfig {
			c := connector(goodToken)
			c.WebhookUsername = "esf-hooks"
			c.WebhookSecretFile = goodToken
			c.WebhookTLSCertFile = filepath.Join(t.TempDir(), "missing.crt")
			c.WebhookTLSKeyFile = filepath.Join(t.TempDir(), "missing.key")
			return c
		}()}, "fail", "webhook TLS"},
		{"webhook listener without dedicated secret", map[string]factory.ConnectorConfig{"azdo": func() factory.ConnectorConfig {
			c := connector(goodToken)
			c.WebhookListen = "127.0.0.1:8091"
			return c
		}()}, "fail", "webhook_secret_file"},
		{"missing webhook secret file", map[string]factory.ConnectorConfig{"azdo": func() factory.ConnectorConfig {
			c := connector(goodToken)
			c.WebhookUsername = "esf-hooks"
			c.WebhookSecretFile = filepath.Join(t.TempDir(), "missing.secret")
			return c
		}()}, "fail", "webhook_secret_file"},
		{"group readable webhook secret", map[string]factory.ConnectorConfig{"azdo": func() factory.ConnectorConfig {
			c := connector(goodToken)
			c.WebhookUsername = "esf-hooks"
			c.WebhookSecretFile = writeDoctorToken(t, 0o644)
			return c
		}()}, "fail", "permissions"},
		{"valid webhook configuration", map[string]factory.ConnectorConfig{"azdo": func() factory.ConnectorConfig {
			c := connector(goodToken)
			c.WebhookUsername = "esf-hooks"
			c.WebhookSecretFile = goodToken
			return c
		}()}, "ok", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := productionConnectorConfiguration(factory.Config{Connectors: tc.connectors})
			if check.Status != tc.status {
				t.Fatalf("status = %q, want %q (%s)", check.Status, tc.status, check.Detail)
			}
			if tc.want != "" && !strings.Contains(check.Detail, tc.want) {
				t.Fatalf("detail = %q, want %q", check.Detail, tc.want)
			}
		})
	}
}

func TestProductionTokenFilePermissionsCheck(t *testing.T) {
	t.Parallel()

	goodToken := writeDoctorToken(t, 0o600)
	groupReadable := writeDoctorToken(t, 0o640)

	t.Run("valid API and connector tokens", func(t *testing.T) {
		t.Parallel()
		cfg := doctorAPIConfig(goodToken)
		cfg.Connectors = map[string]factory.ConnectorConfig{
			"azdo": {Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme", TokenFile: goodToken},
		}
		if check := productionTokenFiles(cfg); check.Status != "ok" {
			t.Fatalf("check = %#v, want ok", check)
		}
	})

	t.Run("connector token readable outside owner", func(t *testing.T) {
		t.Parallel()
		cfg := factory.Config{Connectors: map[string]factory.ConnectorConfig{
			"azdo": {Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme", TokenFile: groupReadable},
		}}
		check := productionTokenFiles(cfg)
		if check.Status != "fail" || !strings.Contains(check.Detail, `connector "azdo"`) || !strings.Contains(check.Detail, "permissions") {
			t.Fatalf("check = %#v, want a named permission failure", check)
		}
	})

	t.Run("no token files", func(t *testing.T) {
		t.Parallel()
		if check := productionTokenFiles(factory.Config{}); check.Status != "skipped" {
			t.Fatalf("check = %#v, want skipped", check)
		}
	})
}

func TestProductionEvidenceSigningKeyCheck(t *testing.T) {
	t.Parallel()

	valid := writeDoctorSigningKey(t, 0o600)
	groupReadable := writeDoctorSigningKey(t, 0o640)
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalid, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		path   string
		status string
		want   string
	}{
		{"missing configuration", "", "fail", "required"},
		{"missing file", filepath.Join(t.TempDir(), "missing.pem"), "fail", "no such file"},
		{"group readable", groupReadable, "fail", "permissions"},
		{"invalid material", invalid, "fail", "PEM encoded"},
		{"valid", valid, "ok", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			check := productionEvidenceSigningKey(factory.Config{Evidence: factory.EvidenceConfig{SigningKeyFile: tc.path}})
			if check.Status != tc.status {
				t.Fatalf("status = %q, want %q (%s)", check.Status, tc.status, check.Detail)
			}
			if tc.want != "" && !strings.Contains(check.Detail, tc.want) {
				t.Fatalf("detail = %q, want %q", check.Detail, tc.want)
			}
		})
	}
}
