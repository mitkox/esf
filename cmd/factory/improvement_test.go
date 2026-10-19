package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/factory"
	enumspb "go.temporal.io/api/enums/v1"
)

func TestImprovementCLISelectionIsNamedOnly(t *testing.T) {
	root := rootCommand()
	run, _, e := root.Find([]string{"run"})
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"harness-profile", "improvement-policy"} {
		if run.Flags().Lookup(name) == nil {
			t.Fatal("missing named selector", name)
		}
	}
	for _, name := range []string{"profile", "improve", "budget-gateway"} {
		cmd, _, e := root.Find([]string{name})
		if e != nil || cmd == root {
			t.Fatal("missing command", name)
		}
	}
	cfg := factory.Default()
	spec := runSpec{RunID: "task", Repository: "https://example.com/repo", Revision: strings.Repeat("a", 40), Task: "change", AgentHarness: "opencode2", VerificationProfile: "default", HarnessProfile: "approved", ImprovementPolicy: "pilot"}
	req, e := runRequestFromSpec(cfg, spec)
	if e != nil {
		t.Fatal(e)
	}
	if req.HarnessProfile != "approved" || req.ImprovementPolicy != "pilot" {
		t.Fatal("apply dropped named selectors")
	}
	if improvementReusePolicy(req) != enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE {
		t.Fatal("completed improvement IDs can be resubmitted")
	}
}
func TestGatewayCannotBindPublicInterfaces(t *testing.T) {
	var cfg string
	cmd := newBudgetGatewayCommand(&cfg)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--listen", "0.0.0.0:8093", "--state", t.TempDir(), "--upstream", "https://example.invalid/v1", "--upstream-key-env", "UNSET"})
	if e := cmd.Execute(); e == nil || !strings.Contains(e.Error(), "loopback") {
		t.Fatal("public binding accepted", e)
	}
}
