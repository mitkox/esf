package console

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/factory"
	"github.com/mitkox/esf/internal/forge"
)

func connectorHealthFixtureConfig(cfg *Config, tokenFile string) {
	cfg.Factory.Connectors = map[string]factory.ConnectorConfig{
		"azdo": {
			Kind:      "azuredevops",
			BaseURL:   "https://dev.azure.com/acme",
			TokenFile: tokenFile,
		},
	}
	cfg.Factory.Scopes["payments"] = factory.ScopeConfig{}
	cfg.Factory.Pipelines = map[string]factory.PipelineConfig{
		"bugfix": {
			Connector: "azdo",
			Rules: []factory.PipelineRule{{
				Project:             "Payments",
				Type:                "Bug",
				Scope:               "payments",
				Repository:          "https://dev.azure.com/acme/payments/_git/payments",
				BaseBranch:          "main",
				VerificationProfile: "default",
			}},
		},
	}
}

func TestConnectorHealthEndpointIsReadOnlyAndAllowListed(t *testing.T) {
	root, clock := buildFixtureStore(t)
	tokenFile := filepath.Join(root, "connector-token-secret-path")
	sweep := clock.current.Add(-2 * time.Hour)
	delivery := sweep.Add(-time.Hour)
	report := forge.ConnectorHealth{
		Connector:           "azdo",
		Status:              forge.HealthDegraded,
		Reason:              "subscription_disabled",
		Detail:              "the forge reports the event subscription is disabled",
		Subscription:        forge.SubscriptionDisabled,
		SubscriptionDetail:  "the creator left the organization",
		LastSuccessfulSweep: &sweep,
		LastDelivery:        &delivery,
		QueueDepth:          map[string]int{"payments": 3},
	}
	if err := forge.SaveConnectorHealth(root, report); err != nil {
		t.Fatalf("SaveConnectorHealth: %v", err)
	}

	server := newTestServer(t, root, clock, func(cfg *Config) {
		connectorHealthFixtureConfig(cfg, tokenFile)
	})
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/connectors", testToken)
	if status != http.StatusOK {
		t.Fatalf("GET connectors status = %d: %s", status, body)
	}
	var response ConnectorsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode connectors: %v\n%s", err, body)
	}
	if len(response.Connectors) != 1 {
		t.Fatalf("connectors = %+v", response.Connectors)
	}
	connector := response.Connectors[0]
	if connector.Name != "azdo" || connector.Kind != "azuredevops" || connector.BaseURL != "https://dev.azure.com/acme" {
		t.Fatalf("connector identity = %+v", connector)
	}
	if connector.Status != string(forge.HealthDegraded) || connector.Reason != "subscription_disabled" {
		t.Fatalf("connector health = %+v", connector)
	}
	if connector.Subscription != string(forge.SubscriptionDisabled) ||
		!strings.Contains(connector.SubscriptionDetail, "creator left") {
		t.Fatalf("connector subscription = %+v", connector)
	}
	if connector.LastSuccessfulSweep == nil || !connector.LastSuccessfulSweep.Equal(sweep) {
		t.Fatalf("last successful sweep = %v", connector.LastSuccessfulSweep)
	}
	if connector.LastDelivery == nil || !connector.LastDelivery.Equal(delivery) {
		t.Fatalf("last delivery = %v", connector.LastDelivery)
	}
	if len(connector.Pipelines) != 1 || connector.Pipelines[0] != "bugfix" {
		t.Fatalf("pipelines = %v", connector.Pipelines)
	}
	if len(connector.Scopes) != 1 || connector.Scopes[0] != "payments" {
		t.Fatalf("scopes = %v", connector.Scopes)
	}
	if len(connector.Queues) != 1 || connector.Queues[0].Scope != "payments" || connector.Queues[0].Depth != 3 {
		t.Fatalf("queues = %+v", connector.Queues)
	}
	if strings.Contains(string(body), "connector-token-secret-path") || strings.Contains(string(body), "token_file") {
		t.Fatalf("connector health response leaked a credential reference: %s", body)
	}

	status, _, _ = request(t, server, http.MethodPost, "/api/v1/console/connectors", testToken)
	if status == http.StatusOK || status == http.StatusCreated {
		t.Fatalf("POST connectors status = %d, the console exposed a write surface", status)
	}
}

func TestConnectorHealthEndpointReportsMissingObservation(t *testing.T) {
	root, clock := buildFixtureStore(t)
	server := newTestServer(t, root, clock, func(cfg *Config) {
		connectorHealthFixtureConfig(cfg, filepath.Join(root, "token"))
	})
	status, body, _ := request(t, server, http.MethodGet, "/api/v1/console/connectors", testToken)
	if status != http.StatusOK {
		t.Fatalf("GET connectors status = %d: %s", status, body)
	}
	var response ConnectorsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode connectors: %v", err)
	}
	if len(response.Connectors) != 1 {
		t.Fatalf("connectors = %+v", response.Connectors)
	}
	connector := response.Connectors[0]
	if connector.Status != string(forge.HealthUnknown) || connector.Reason != "no_observations" {
		t.Fatalf("unobserved connector health = %+v", connector)
	}
	if connector.Subscription != string(forge.SubscriptionUnknown) {
		t.Fatalf("unobserved subscription = %q", connector.Subscription)
	}
}
