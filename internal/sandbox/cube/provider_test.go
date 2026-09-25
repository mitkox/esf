package cube

import (
	"testing"

	"github.com/mitkox/esf/internal/sandbox"
)

func TestCubeNetworkRulesPreserveCredentialPolicy(t *testing.T) {
	rules := cubeNetworkRules([]sandbox.NetworkRule{{
		Name: "model",
		Match: sandbox.NetworkMatch{
			Scheme: "https", Host: "api.example.com", SNI: "api.example.com",
			Method: []string{"POST"}, Path: "/v1/*", Port: 8443,
		},
		Action: sandbox.NetworkAction{
			Allow: true, Audit: "metadata",
			Inject: []sandbox.HeaderInjection{{Header: "Authorization", Secret: "secret", Format: "Bearer ${SECRET}"}},
		},
	}})
	if len(rules) != 1 {
		t.Fatalf("rules = %d", len(rules))
	}
	got := rules[0]
	if got.Match.Host != "api.example.com" || got.Match.Port != 8443 || got.Match.Path != "/v1/*" {
		t.Fatalf("match = %+v", got.Match)
	}
	if len(got.Action.Inject) != 1 || got.Action.Inject[0].Secret != "secret" || got.Action.Inject[0].Format != "Bearer ${SECRET}" {
		t.Fatalf("inject = %+v", got.Action.Inject)
	}
}
