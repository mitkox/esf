package factory

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPayloadKeyringRequiresPrivateRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.json")
	body := []byte(fmt.Sprintf(`{"active":"key","keys":{"key":%q}}`, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))))
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPayloadCodec(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPayloadCodec(path); err == nil {
		t.Fatal("public keyring accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPayloadCodec(link); err == nil {
		t.Fatal("symlink keyring accepted")
	}
}
