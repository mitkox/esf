package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConsoleTokenFile(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid.token")
	token := strings.Repeat("a", 64)
	if err := os.WriteFile(valid, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	loaded, err := readConsoleTokenFile(valid)
	if err != nil || loaded != token {
		t.Fatalf("readConsoleTokenFile = %q, %v", loaded, err)
	}

	exposed := filepath.Join(directory, "exposed.token")
	if err := os.WriteFile(exposed, []byte(token), 0o644); err != nil {
		t.Fatalf("write exposed token: %v", err)
	}
	if _, err := readConsoleTokenFile(exposed); err == nil || !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("expected a permissions error, got %v", err)
	}

	short := filepath.Join(directory, "short.token")
	if err := os.WriteFile(short, []byte("too-short"), 0o600); err != nil {
		t.Fatalf("write short token: %v", err)
	}
	if _, err := readConsoleTokenFile(short); err == nil {
		t.Fatalf("expected a short-token error")
	}

	link := filepath.Join(directory, "link.token")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := readConsoleTokenFile(link); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected a symlink rejection, got %v", err)
	}

	if _, err := readConsoleTokenFile(filepath.Join(directory, "missing")); err == nil {
		t.Fatalf("expected an error for a missing file")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for address, expected := range map[string]bool{
		"127.0.0.1:7331":    true,
		"localhost:7331":    true,
		"[::1]:7331":        true,
		"0.0.0.0:7331":      false,
		":7331":             false,
		"192.168.0.10:7331": false,
	} {
		if got := isLoopbackHost(address); got != expected {
			t.Fatalf("isLoopbackHost(%q) = %v, want %v", address, got, expected)
		}
	}
}

func TestParseConsoleRefresh(t *testing.T) {
	got, err := parseConsoleRefresh("30s")
	if err != nil || got.Seconds() != 30 {
		t.Fatalf("parseConsoleRefresh(30s) = %v, %v", got, err)
	}
	if _, err := parseConsoleRefresh("nonsense"); err == nil {
		t.Fatalf("expected an error for an invalid --refresh value")
	}
	if _, err := parseConsoleRefresh("10ms"); err == nil {
		t.Fatalf("expected an error for a sub-second --refresh value")
	}
}

func TestResolveConsoleTLSFlags(t *testing.T) {
	t.Setenv("FACTORY_CONSOLE_TLS_CERT_FILE", " /env/console.crt ")
	t.Setenv("FACTORY_CONSOLE_TLS_KEY_FILE", " /env/console.key ")

	cert, key := resolveConsoleTLSFlags("", "")
	if cert != "/env/console.crt" || key != "/env/console.key" {
		t.Fatalf("environment defaults = %q, %q", cert, key)
	}
	cert, key = resolveConsoleTLSFlags("/flag/console.crt", "")
	if cert != "/flag/console.crt" || key != "/env/console.key" {
		t.Fatalf("flag precedence = %q, %q", cert, key)
	}
	cert, key = resolveConsoleTLSFlags("", "/flag/console.key")
	if cert != "/env/console.crt" || key != "/flag/console.key" {
		t.Fatalf("mixed precedence = %q, %q", cert, key)
	}

	t.Setenv("FACTORY_CONSOLE_TLS_CERT_FILE", "/env/console.crt")
	t.Setenv("FACTORY_CONSOLE_TLS_KEY_FILE", "")
	cert, key = resolveConsoleTLSFlags("", "")
	if cert != "/env/console.crt" || key != "" {
		t.Fatalf("half-configured TLS = %q, %q", cert, key)
	}
}
