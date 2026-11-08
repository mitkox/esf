package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetentionNeverDeletesCompletedBundlesOrSignatures(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-2")
	for _, path := range []string{
		filepath.Join(runDir, "run.json"),
		filepath.Join(runDir, "evidence", "bundle.json"),
		filepath.Join(runDir, "evidence", "signature.json"),
		filepath.Join(runDir, "evidence", ".artifact-interrupted"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-90 * 24 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	items, err := findOrphanArtifacts(root, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || filepath.Base(items[0].path) != ".artifact-interrupted" {
		t.Fatalf("retention candidates = %+v, want only the interrupted atomic write", items)
	}
	if err := applyOrphanArtifacts(items); err != nil {
		t.Fatalf("applyOrphanArtifacts: %v", err)
	}
	for _, name := range []string{"run.json", filepath.Join("evidence", "bundle.json"), filepath.Join("evidence", "signature.json")} {
		if _, err := os.Stat(filepath.Join(runDir, name)); err != nil {
			t.Fatalf("completed evidence %s was deleted: %v", name, err)
		}
	}
}

func TestRetentionKeepsEvidenceAndRejectsChangedCandidates(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	for _, name := range []string{".artifact-interrupted", "run.json", "changes.patch"} {
		path := filepath.Join(runDir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	items, err := findOrphanArtifacts(root, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || filepath.Base(items[0].path) != ".artifact-interrupted" {
		t.Fatalf("retention candidates: %+v", items)
	}
	plan := makeRetentionPlan(root, 30, items)
	if plan.Files != 1 || plan.Bytes != int64(len(".artifact-interrupted")) || len(plan.PlanDigest) != 64 {
		t.Fatalf("invalid plan: %+v", plan)
	}
	if err := os.WriteFile(items[0].path, []byte("new content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyOrphanArtifacts(items); err == nil {
		t.Fatal("changed candidate was deleted")
	}
	if err := os.Chtimes(items[0].path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(items[0].path, []byte(".artifact-interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(items[0].path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := applyOrphanArtifacts(items); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(items[0].path); !os.IsNotExist(err) {
		t.Fatalf("orphan still present: %v", err)
	}
	for _, name := range []string{"run.json", "changes.patch"} {
		if _, err := os.Stat(filepath.Join(runDir, name)); err != nil {
			t.Fatalf("protected evidence %s: %v", name, err)
		}
	}
}
