package factory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/forge"
	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/tomlx"
)

func forgeTestConfig() Config {
	return Config{
		Cube:     cube.Config{APIURL: "http://127.0.0.1:4000", TemplateID: "tpl", IdleTimeout: tomlx.FromStd(time.Minute), RequestTimeout: tomlx.FromStd(time.Minute)},
		Temporal: TemporalConfig{HostPort: "127.0.0.1:7233", TaskQueue: "factory"},
		Storage:  StorageConfig{DataDir: ".factory"},
		Harnesses: map[string]HarnessConfig{
			"a": {Type: "generic", Executable: "a", Timeout: tomlx.FromStd(time.Minute)},
		},
		Verification: defaultVerificationProfiles(),
		Limits:       LimitsConfig{AgentTimeout: tomlx.FromStd(time.Minute), TotalTimeout: tomlx.FromStd(time.Hour)},
		Hardening:    HardeningConfig{AcknowledgeOpenEgress: true},
		Scopes: map[string]ScopeConfig{
			"payments": {},
		},
		Connectors: map[string]ConnectorConfig{
			"azdo": {
				Kind:      "azuredevops",
				BaseURL:   "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token",
			},
		},
	}
}

func forgeTestRule(project, scope, repository, branch, profile string) PipelineRule {
	return PipelineRule{
		Project:             project,
		Scope:               scope,
		Repository:          repository,
		BaseBranch:          branch,
		VerificationProfile: profile,
	}
}

func TestForgeConfigValidation(t *testing.T) {
	t.Parallel()

	validRule := forgeTestRule("Payments", "payments", "https://dev.azure.com/acme/payments/_git/payments", "main", "default")
	cases := []struct {
		name     string
		pipeline PipelineConfig
		want     []string
	}{
		{
			name:     "valid",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{validRule}},
		},
		{
			name:     "default scope is implicitly available",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{forgeTestRule("Payments", "default", validRule.Repository, "main", "default")}},
		},
		{
			name:     "unknown verification profile",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{forgeTestRule("Payments", "payments", validRule.Repository, "main", "missing")}},
			want:     []string{"unknown verification profile"},
		},
		{
			name:     "unknown scope",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{forgeTestRule("Payments", "missing", validRule.Repository, "main", "default")}},
			want:     []string{"unknown scope"},
		},
		{
			name: "all cross-reference problems accumulate",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{
				forgeTestRule("Payments", "payments", validRule.Repository, "main", "missing"),
				forgeTestRule("Identity", "missing", validRule.Repository, "main", "default"),
			}},
			want: []string{"unknown verification profile", "unknown scope"},
		},
		{
			name:     "unknown connector",
			pipeline: PipelineConfig{Connector: "missing", Rules: []PipelineRule{validRule}},
			want:     []string{"unknown connector"},
		},
		{
			name:     "no mapping rules",
			pipeline: PipelineConfig{Connector: "azdo"},
			want:     []string{"at least one mapping rule"},
		},
		{
			name:     "missing repository",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{forgeTestRule("Payments", "payments", "", "main", "default")}},
			want:     []string{"repository is required"},
		},
		{
			name:     "missing base branch",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{forgeTestRule("Payments", "payments", validRule.Repository, "", "default")}},
			want:     []string{"base_branch is required"},
		},
		{
			name:     "missing verification profile",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{forgeTestRule("Payments", "payments", validRule.Repository, "main", "")}},
			want:     []string{"verification_profile is required"},
		},
		{
			name: "identical selectors with different targets are ambiguous",
			pipeline: PipelineConfig{Connector: "azdo", Rules: []PipelineRule{
				forgeTestRule("Payments", "payments", "https://dev.azure.com/acme/payments/_git/one", "main", "default"),
				forgeTestRule("Payments", "payments", "https://dev.azure.com/acme/payments/_git/two", "main", "default"),
			}},
			want: []string{"ambiguous"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := forgeTestConfig()
			cfg.Pipelines = map[string]PipelineConfig{"bugfix": tc.pipeline}
			err := cfg.Validate()
			if len(tc.want) == 0 {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate accepted %s", tc.name)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Validate() = %v, want %q", err, want)
				}
			}
		})
	}
}

func TestConnectorConfigValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		connector ConnectorConfig
		want      string
	}{
		{
			name:      "valid",
			connector: ConnectorConfig{Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme", TokenFile: "/run/secrets/azdo.token"},
		},
		{
			name:      "loopback test endpoint",
			connector: ConnectorConfig{Kind: "github", BaseURL: "http://127.0.0.1:8080", TokenFile: "/run/secrets/github.token"},
		},
		{
			name:      "unknown kind",
			connector: ConnectorConfig{Kind: "jenkins", BaseURL: "https://jenkins.example", TokenFile: "/run/secrets/token"},
			want:      "kind",
		},
		{
			name:      "relative token path",
			connector: ConnectorConfig{Kind: "gitlab", BaseURL: "https://gitlab.example", TokenFile: "token"},
			want:      "token_file must be an absolute path",
		},
		{
			name:      "missing token",
			connector: ConnectorConfig{Kind: "gitlab", BaseURL: "https://gitlab.example"},
			want:      "token_file is required",
		},
		{
			name:      "plaintext remote endpoint",
			connector: ConnectorConfig{Kind: "gitlab", BaseURL: "http://gitlab.example", TokenFile: "/run/secrets/token"},
			want:      "base_url must use HTTPS",
		},
		{
			name:      "embedded endpoint credentials",
			connector: ConnectorConfig{Kind: "gitlab", BaseURL: "https://user:pass@gitlab.example", TokenFile: "/run/secrets/token"},
			want:      "base_url must be an absolute URL",
		},
		{
			name: "custom webhook listener and TLS",
			connector: ConnectorConfig{
				Kind:               "azuredevops",
				BaseURL:            "https://dev.azure.com/acme",
				TokenFile:          "/run/secrets/azdo.token",
				WebhookUsername:    "esf-hooks",
				WebhookSecretFile:  "/run/secrets/azdo-webhook.secret",
				WebhookListen:      "127.0.0.1:9091",
				WebhookTLSCertFile: "/run/secrets/tls.crt",
				WebhookTLSKeyFile:  "/run/secrets/tls.key",
				SweepInterval:      tomlx.FromStd(30 * time.Second),
			},
		},
		{
			name: "plaintext remote webhook",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "0.0.0.0:8091",
			},
			want: "non-loopback",
		},
		{
			name: "malformed webhook listener",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "localhost",
			},
			want: "listen address",
		},
		{
			name: "webhook TLS pair required",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookTLSCertFile: "/run/secrets/tls.crt",
			},
			want: "set together",
		},
		{
			name: "webhook listener without dedicated credential",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "127.0.0.1:9091",
			},
			want: "webhook_username and webhook_secret_file are required",
		},
		{
			name: "webhook username without secret file",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "127.0.0.1:9091",
				WebhookUsername: "esf-hooks",
			},
			want: "webhook_secret_file is required",
		},
		{
			name: "relative webhook secret file",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "127.0.0.1:9091",
				WebhookUsername: "esf-hooks", WebhookSecretFile: "webhook.secret",
			},
			want: "webhook_secret_file must be an absolute path",
		},
		{
			name: "webhook username separator",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "127.0.0.1:9091",
				WebhookUsername: "esf:hooks", WebhookSecretFile: "/run/secrets/webhook.secret",
			},
			want: "webhook_username must not contain",
		},
		{
			name: "webhook secret is the outbound token",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "127.0.0.1:9091",
				WebhookUsername: "esf-hooks", WebhookSecretFile: "/run/secrets/azdo.token",
			},
			want: "webhook_secret_file must not name the same file as token_file",
		},
		{
			name: "webhook secret cleans to the outbound token",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", WebhookListen: "127.0.0.1:9091",
				WebhookUsername: "esf-hooks", WebhookSecretFile: "/run/secrets/../secrets/./azdo.token",
			},
			want: "webhook_secret_file must not name the same file as token_file",
		},
		{
			name: "negative sweep interval",
			connector: ConnectorConfig{
				Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
				TokenFile: "/run/secrets/azdo.token", SweepInterval: tomlx.FromStd(-time.Second),
			},
			want: "sweep_interval",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := forgeTestConfig()
			cfg.Connectors = map[string]ConnectorConfig{"forge": tc.connector}
			err := cfg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}

// TestConnectorWebhookListenersMustBeDistinct keeps the runtime from starting
// one receiver that would collide with another connector's bind.
func TestConnectorWebhookListenersMustBeDistinct(t *testing.T) {
	t.Parallel()

	cfg := forgeTestConfig()
	first := cfg.Connectors["azdo"]
	first.WebhookListen = "127.0.0.1:8091"
	first.WebhookUsername = "esf-hooks"
	first.WebhookSecretFile = "/run/secrets/azdo-webhook.secret"
	cfg.Connectors["azdo"] = first
	cfg.Connectors["second"] = ConnectorConfig{
		Kind: "azuredevops", BaseURL: "https://dev.azure.com/other", TokenFile: "/run/secrets/other.token",
		WebhookUsername:   "other-hooks",
		WebhookSecretFile: "/run/secrets/other-webhook.secret",
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "both bind webhook_listen") {
		t.Fatalf("Validate() = %v, want a duplicate webhook listener error", err)
	}
	cfg.Connectors["second"] = func() ConnectorConfig {
		connector := cfg.Connectors["second"]
		connector.WebhookListen = "127.0.0.1:8092"
		return connector
	}()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("distinct listeners must be valid: %v", err)
	}
}

func TestConnectorRuntimeDefaults(t *testing.T) {
	t.Parallel()

	var connector ConnectorConfig
	if got := connector.WebhookListenAddress(); got != DefaultConnectorWebhookListen {
		t.Fatalf("webhook listen = %q, want %q", got, DefaultConnectorWebhookListen)
	}
	if got := connector.SweepIntervalOrDefault(); got != DefaultConnectorSweepInterval {
		t.Fatalf("sweep interval = %s, want %s", got, DefaultConnectorSweepInterval)
	}
	if connector.WebhookTLSConfigured() {
		t.Fatal("TLS reported configured without both files")
	}
	connector.WebhookTLSCertFile = "/run/secrets/tls.crt"
	connector.WebhookTLSKeyFile = "/run/secrets/tls.key"
	if !connector.WebhookTLSConfigured() {
		t.Fatal("TLS pair not reported configured")
	}
}

// TestPipelineRuleMustMatchScopeVerificationProfile proves the startup check
// catches a rule whose gate profile the scope it maps to would reject at run
// time.
func TestPipelineRuleMustMatchScopeVerificationProfile(t *testing.T) {
	t.Parallel()

	cfg := forgeTestConfig()
	cfg.Verification["strict"] = cfg.Verification["default"]
	cfg.Scopes["payments"] = ScopeConfig{VerificationProfile: "strict"}
	cfg.Pipelines = map[string]PipelineConfig{
		"bugfix": {
			Connector: "azdo",
			Rules:     []PipelineRule{forgeTestRule("Payments", "payments", "https://operator.example/payments", "main", "default")},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "does not match scope") {
		t.Fatalf("Validate() = %v, want a scope/profile mismatch", err)
	}
}

func TestResolvePipelineUsesMostSpecificOperatorMapping(t *testing.T) {
	t.Parallel()

	cfg := forgeTestConfig()
	cfg.Pipelines = map[string]PipelineConfig{
		"bugfix": {
			Connector: "azdo",
			Rules: []PipelineRule{
				forgeTestRule("Payments", "payments", "https://operator.example/payments/general", "main", "default"),
				{
					Project:             "Payments",
					Label:               "esf",
					Type:                "Bug",
					Scope:               "payments",
					Repository:          "https://operator.example/payments/bugs",
					BaseBranch:          "bugfix",
					VerificationProfile: "default",
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	general, err := cfg.ResolvePipeline("bugfix", forge.WorkItemFields{Project: "Payments"})
	if err != nil {
		t.Fatalf("ResolvePipeline(general): %v", err)
	}
	if general.Repository != "https://operator.example/payments/general" || general.BaseBranch != "main" {
		t.Fatalf("general mapping = %#v", general)
	}

	specific, err := cfg.ResolvePipeline("bugfix", forge.WorkItemFields{
		Project: "Payments",
		Type:    "Bug",
		Labels:  []string{"esf"},
	})
	if err != nil {
		t.Fatalf("ResolvePipeline(specific): %v", err)
	}
	if specific.Repository != "https://operator.example/payments/bugs" || specific.BaseBranch != "bugfix" {
		t.Fatalf("specific mapping = %#v", specific)
	}
	if specific.Scope != "payments" || specific.VerificationProfile != "default" {
		t.Fatalf("specific target = %#v", specific)
	}
}

func TestResolvePipelineIgnoresPayloadRepository(t *testing.T) {
	t.Parallel()

	cfg := forgeTestConfig()
	cfg.Pipelines = map[string]PipelineConfig{
		"bugfix": {
			Connector: "azdo",
			Rules: []PipelineRule{
				{
					Project:             "Payments",
					Type:                "Bug",
					Scope:               "payments",
					Repository:          "https://operator.example/payments/bugs",
					BaseBranch:          "main",
					VerificationProfile: "default",
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	payload := map[string]string{
		"repository": "https://attacker.example/exfiltrate",
		"project":    "Payments",
		"type":       "Bug",
	}
	// WorkItemFields is the complete mapping input and has no repository field,
	// so a delivered payload cannot select where the change lands.
	if _, ok := reflect.TypeOf(forge.WorkItemFields{}).FieldByName("Repository"); ok {
		t.Fatal("the mapping input must not carry a repository chosen by a payload")
	}

	resolved, err := cfg.ResolvePipeline("bugfix", forge.WorkItemFields{
		Project: payload["project"],
		Type:    payload["type"],
	})
	if err != nil {
		t.Fatalf("ResolvePipeline: %v", err)
	}
	if resolved.Repository != "https://operator.example/payments/bugs" {
		t.Fatalf("repository = %q, want the operator mapping", resolved.Repository)
	}
}

func TestResolvePipelineRejectsNoMatchAndAmbiguity(t *testing.T) {
	t.Parallel()

	baseRule := forgeTestRule("Payments", "payments", "https://operator.example/payments", "main", "default")

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		cfg := forgeTestConfig()
		cfg.Pipelines = map[string]PipelineConfig{"bugfix": {Connector: "azdo", Rules: []PipelineRule{baseRule}}}
		if _, err := cfg.ResolvePipeline("bugfix", forge.WorkItemFields{Project: "Identity"}); err == nil || !strings.Contains(err.Error(), "no mapping rule matches") {
			t.Fatalf("ResolvePipeline() = %v, want a no-match error", err)
		}
	})

	t.Run("equally specific different targets", func(t *testing.T) {
		t.Parallel()
		cfg := forgeTestConfig()
		cfg.Pipelines = map[string]PipelineConfig{
			"bugfix": {
				Connector: "azdo",
				Rules: []PipelineRule{
					{
						Project:             "Payments",
						Label:               "esf",
						Scope:               "payments",
						Repository:          "https://operator.example/by-label",
						BaseBranch:          "main",
						VerificationProfile: "default",
					},
					{
						Project:             "Payments",
						Type:                "Bug",
						Scope:               "payments",
						Repository:          "https://operator.example/by-type",
						BaseBranch:          "main",
						VerificationProfile: "default",
					},
				},
			},
		}
		_, err := cfg.ResolvePipeline("bugfix", forge.WorkItemFields{Project: "Payments", Type: "Bug", Labels: []string{"esf"}})
		if err == nil || !strings.Contains(err.Error(), "equally specific") {
			t.Fatalf("ResolvePipeline() = %v, want an ambiguity error", err)
		}
	})
}

func TestConnectorConstructionFailsClosedOnUnsafeTokenFile(t *testing.T) {
	t.Parallel()

	cfg := forgeTestConfig()
	dir := t.TempDir()
	good := filepath.Join(dir, "token")
	if err := os.WriteFile(good, []byte("secret-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	groupReadable := filepath.Join(dir, "group-readable")
	if err := os.WriteFile(groupReadable, []byte("secret-token"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{groupReadable, link, filepath.Join(dir, "missing")} {
		cfg.Connectors["azdo"] = ConnectorConfig{
			Kind:      "azuredevops",
			BaseURL:   "https://dev.azure.com/acme",
			TokenFile: path,
		}
		if _, err := cfg.Connector("azdo"); err == nil || !strings.Contains(err.Error(), "token_file") {
			t.Fatalf("Connector(%q) = %v, want a token_file error", path, err)
		}
	}

	cfg.Connectors["azdo"] = ConnectorConfig{
		Kind:      "azuredevops",
		BaseURL:   "https://dev.azure.com/acme",
		TokenFile: good,
	}
	settings, err := cfg.Connector("azdo")
	if err != nil {
		t.Fatalf("Connector: %v", err)
	}
	if settings.Name != "azdo" || settings.Kind != forge.KindAzureDevOps || settings.BaseURL != "https://dev.azure.com/acme" {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.Credential.Value() != "secret-token" {
		t.Fatalf("credential = %q", settings.Credential.Value())
	}

	cfg.Connectors = map[string]ConnectorConfig{}
	if _, err := cfg.Connector("azdo"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Connector(unknown) = %v", err)
	}
}

func TestEmptyConnectorListDoesNotInvalidateConfiguration(t *testing.T) {
	t.Parallel()

	cfg := forgeTestConfig()
	cfg.Connectors = nil
	cfg.Pipelines = nil
	cfg.API = APIConfig{Enabled: true, Listen: "127.0.0.1:8090", TokenFile: "/run/secrets/api.token"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("an empty connector list must not invalidate the API configuration: %v", err)
	}
	if _, err := cfg.ResolvePipeline("bugfix", forge.WorkItemFields{}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("ResolvePipeline() = %v, want a configuration error at submission", err)
	}
}

func TestLoadConfigForgeSections(t *testing.T) {
	t.Setenv("CUBE_API_URL", "http://127.0.0.1:4000")
	t.Setenv("CUBE_TEMPLATE_ID", "tpl-test")

	dir := t.TempDir()
	path := filepath.Join(dir, "factory.toml")
	body := `
[hardening]
acknowledge_open_egress = true

[scopes.payments]

[connectors.azuredevops]
kind = "azuredevops"
base_url = "https://dev.azure.com/acme"
token_file = "/run/secrets/azdo.token"
webhook_username = "esf-hooks"
webhook_secret_file = "/run/secrets/azdo-webhook.secret"
webhook_listen = "127.0.0.1:8092"
webhook_tls_cert_file = "/run/secrets/tls.crt"
webhook_tls_key_file = "/run/secrets/tls.key"
sweep_interval = "30s"

[pipelines.bugfix]
connector = "azuredevops"

[[pipelines.bugfix.rules]]
project = "Payments"
type = "Bug"
label = "esf"
state = "Active"
scope = "payments"
repository = "https://dev.azure.com/acme/payments/_git/payments"
base_branch = "main"
verification_profile = "default"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loaded configuration is invalid: %v", err)
	}
	if got := cfg.Connectors["azuredevops"].Kind; got != "azuredevops" {
		t.Fatalf("connector kind = %q", got)
	}
	connector := cfg.Connectors["azuredevops"]
	if connector.WebhookListenAddress() != "127.0.0.1:8092" ||
		connector.WebhookUsername != "esf-hooks" ||
		connector.WebhookSecretFile != "/run/secrets/azdo-webhook.secret" ||
		connector.WebhookTLSCertFile != "/run/secrets/tls.crt" ||
		connector.WebhookTLSKeyFile != "/run/secrets/tls.key" ||
		connector.SweepIntervalOrDefault() != 30*time.Second {
		t.Fatalf("connector runtime config = %#v", connector)
	}
	rules := cfg.Pipelines["bugfix"].Rules
	if len(rules) != 1 || rules[0].Project != "Payments" || rules[0].Label != "esf" || rules[0].BaseBranch != "main" {
		t.Fatalf("pipeline rules = %#v", rules)
	}

	unknown := []struct {
		name    string
		snippet string
	}{
		{"connector", "\n[connectors.unexpected]\nkind = \"github\"\nbase_url = \"https://api.github.com\"\ntoken_file = \"/run/secrets/other.token\"\nsurprise_option = true\n"},
		{"pipeline", "\n[pipelines.unexpected]\nconnector = \"azuredevops\"\nsurprise_option = true\n"},
	}
	for _, tc := range unknown {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "factory.toml")
			if err := os.WriteFile(path, []byte(body+tc.snippet), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatalf("LoadConfig accepted an unknown %s key", tc.name)
			}
		})
	}
}

func TestConnectorWebhookUsesDedicatedSecret(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "azdo.token")
	secretFile := filepath.Join(dir, "azdo-webhook.secret")
	if err := os.WriteFile(tokenFile, []byte("esf:pat"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("hook-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	connector := ConnectorConfig{
		Kind: "azuredevops", BaseURL: "https://dev.azure.com/acme",
		TokenFile:         tokenFile,
		WebhookUsername:   "esf-hooks",
		WebhookSecretFile: secretFile,
	}
	cfg := forgeTestConfig()
	cfg.Connectors["azdo"] = connector

	settings, err := cfg.Connector("azdo")
	if err != nil {
		t.Fatalf("Connector: %v", err)
	}
	if settings.Credential.Value() != "esf:pat" {
		t.Fatalf("outbound credential = %q", settings.Credential.Value())
	}
	webhook, err := cfg.ConnectorWebhook("azdo")
	if err != nil {
		t.Fatalf("ConnectorWebhook: %v", err)
	}
	if webhook.Username != "esf-hooks" || webhook.Credential.Value() != "hook-secret" {
		t.Fatalf("webhook credential = %q / %q", webhook.Username, webhook.Credential.Value())
	}

	connector.WebhookSecretFile = ""
	cfg.Connectors["azdo"] = connector
	if _, err := cfg.ConnectorWebhook("azdo"); err == nil || !strings.Contains(err.Error(), "webhook_secret_file") {
		t.Fatalf("ConnectorWebhook without a secret file = %v, want no token_file fallback", err)
	}

	connector.WebhookSecretFile = filepath.Join(dir, "missing")
	cfg.Connectors["azdo"] = connector
	if _, err := cfg.ConnectorWebhook("azdo"); err == nil || !strings.Contains(err.Error(), "webhook_secret_file") {
		t.Fatalf("ConnectorWebhook with a missing secret file = %v", err)
	}
}
