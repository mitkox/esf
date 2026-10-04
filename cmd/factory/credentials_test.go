package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageProjectedCredentials(t *testing.T) {
	for _, variant := range []string{"projected", "escape", "oversized"} {
		t.Run(variant, func(t *testing.T) {
			source := t.TempDir()
			destination := filepath.Join(t.TempDir(), "private")
			data := filepath.Join(source, "..snapshot")
			if err := os.Mkdir(data, 0750); err != nil {
				t.Fatal(err)
			}
			body := "test-only-placeholder"
			if variant == "oversized" {
				body = strings.Repeat("a", (64<<10)+1)
			}
			if variant == "escape" {
				data = t.TempDir()
			}
			if err := os.WriteFile(filepath.Join(data, "key"), []byte(body), 0440); err != nil {
				t.Fatal(err)
			}
			target := "..snapshot"
			if variant == "escape" {
				target = data
			}
			if err := os.Symlink(target, filepath.Join(source, "..data")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("..data/key", filepath.Join(source, "key")); err != nil {
				t.Fatal(err)
			}
			err := stageCredentials(source, destination)
			if variant != "projected" {
				if err == nil {
					t.Fatal("unsafe credential accepted")
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("failed staging left credentials behind")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(destination, "key")
			info, err := os.Lstat(file)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
				t.Fatalf("credential is not private and regular: %v", err)
			}
			contents, err := os.ReadFile(file)
			if err != nil || string(contents) != body {
				t.Fatal("credential contents differ")
			}
			if err := stageCredentials(source, destination); err == nil {
				t.Fatal("overwrote an existing credential directory")
			}
		})
	}
}
