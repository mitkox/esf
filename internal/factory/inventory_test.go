package factory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/sandbox"
)

// The sandbox inventory is the harness contract: an agent discovers its
// environment from one authoritative document instead of parsing prompt text.
// These tests pin the properties that make it safe:
//
//   - the path is derived from the repository directory, not caller input;
//   - the document contains no credential values;
//   - the task text is referenced by path, never copied into the document;
//   - the bytes persisted as evidence are exactly the bytes the agent sees.

func TestInventoryPathDerivesFromRepositoryDir(t *testing.T) {
	cases := []struct {
		repositoryDir string
		want          string
	}{
		{"/workspace/repository", "/workspace/.factory/inventory.json"},
		{"/repo", "/.factory/inventory.json"},
	}
	for _, tc := range cases {
		if got := InventoryPathFor(tc.repositoryDir); got != tc.want {
			t.Errorf("InventoryPathFor(%q) = %q, want %q", tc.repositoryDir, got, tc.want)
		}
	}
	if got := TaskFilePathFor("/workspace/repository"); got != "/workspace/.factory/task.txt" {
		t.Errorf("TaskFilePathFor = %q", got)
	}
}

func TestBuildInventoryCarriesNoCredentialValue(t *testing.T) {
	allowInternet := false
	req := RunRequest{
		RunID:               "run-1",
		ChangeID:            "change-1",
		Repository:          "https://github.com/acme/payments",
		Revision:            "abc",
		Task:                "rotate the API key sk-secret-should-not-appear",
		AgentHarness:        "opencode2",
		VerificationProfile: "default",
		AgentTimeout:        time.Minute,
	}
	resolved := ResolvedResources{
		Scope:           "payments",
		Workspace:       "python",
		WorkspaceDigest: "sha256:abc",
		EgressPolicy:    "locked",
		Egress:          sandbox.Network{AllowInternet: &allowInternet},
		Model:           "fast",
		ModelProvider:   "anthropic",
		ModelID:         "claude-fast",
		ModelAPIKeyEnv:  "ANTHROPIC_API_KEY",
	}
	inventory := BuildInventory(req, resolved, InventoryInput{
		FactoryVersion: "test",
		SandboxID:      "sb-1",
		Template:       "tpl-1",
		Provider:       "fake",
		BaselineSHA:    "deadbeef",
		WrittenAt:      time.Now().UTC(),
	})
	data, err := json.Marshal(inventory)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if strings.Contains(body, "sk-secret") {
		t.Fatal("inventory contains task text; the task must be referenced by path only")
	}
	if inventory.TaskFile == "" {
		t.Fatal("inventory does not reference the task file")
	}
	if !strings.Contains(body, "ANTHROPIC_API_KEY") {
		t.Fatal("inventory should name the credential variable so a harness can read it")
	}
	if inventory.ContractVersion != InventoryContractVersion {
		t.Fatalf("contract version = %q, want %q", inventory.ContractVersion, InventoryContractVersion)
	}
	if inventory.Repository.BaselineSHA != "deadbeef" {
		t.Fatalf("baseline sha = %q", inventory.Repository.BaselineSHA)
	}
	if inventory.Egress.AllowInternet == nil || *inventory.Egress.AllowInternet {
		t.Fatal("egress policy was not carried into the inventory")
	}
}

// TestWriteInventoryDocumentMatchesEvidence proves the agent-visible document
// and the evidence copy are the same bytes.
func TestWriteInventoryDocumentMatchesEvidence(t *testing.T) {
	fake := sandbox.NewFake()
	sb, err := fake.Create(context.Background(), sandbox.Spec{Template: "tpl"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	req := RunRequest{RunID: "run-1", Repository: "https://github.com/acme/x", Revision: "abc", Task: "t"}
	inventory := BuildInventory(req, ResolvedResources{}, InventoryInput{
		FactoryVersion: "test", SandboxID: sb.ID(), Template: "tpl", Provider: "fake", WrittenAt: time.Now().UTC(),
	})
	data, err := WriteInventoryDocument(context.Background(), sb, req.EffectiveRepositoryDir(), inventory)
	if err != nil {
		t.Fatalf("WriteInventoryDocument: %v", err)
	}
	read, err := sb.ReadFile(context.Background(), InventoryPathFor(req.EffectiveRepositoryDir()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(read) != string(data) {
		t.Fatal("the bytes written into the sandbox differ from the bytes returned for evidence")
	}
	var decoded Inventory
	if err := json.Unmarshal(read, &decoded); err != nil {
		t.Fatalf("inventory is not valid JSON: %v", err)
	}
	if decoded.Repository.Directory != req.EffectiveRepositoryDir() {
		t.Fatalf("repository directory = %q", decoded.Repository.Directory)
	}
}
