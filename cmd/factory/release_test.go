package main

import (
	"strings"
	"testing"
)

func TestMigrateConfigTextPreservesSettingsAndComments(t *testing.T) {
	old := "# operator note\nfactory_version = \"0.1.0\"\n[cube]\napi_url = \"http://127.0.0.1:4000\"\n"
	updated, changed, err := migrateConfigText(old)
	if err != nil || !changed {
		t.Fatalf("migration: changed=%v error=%v", changed, err)
	}
	if !strings.HasPrefix(updated, "schema_version = 1\n# operator note") || strings.Contains(updated, "factory_version") || !strings.Contains(updated, "api_url =") {
		t.Fatalf("migration lost settings: %q", updated)
	}
	if _, _, err := migrateConfigText("schema_version = 99\n"); err == nil {
		t.Fatal("accepted newer config schema")
	}
}
