package controlplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mitkox/esf/internal/factory"
)

func TestFactoryReadRequiresSeparateTokenAndOnlyReadsEvidence(t *testing.T) {
	server, web := newTestHTTPServer(t)
	web.Close()
	root := t.TempDir()
	runDir := filepath.Join(root, "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(factory.RunManifest{RunID: "run-1", Patch: factory.ArtifactPatch})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, factory.ArtifactRun), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, factory.ArtifactPatch), []byte("<script>alert(1)</script>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("f", 40)
	tokenPath := filepath.Join(t.TempDir(), "read-token")
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.ConfigureFactoryRead(root, tokenPath); err != nil {
		t.Fatal(err)
	}
	call := func(path, credential string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if credential != "" {
			request.Header.Set("Authorization", "Bearer "+credential)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		return response
	}
	for _, credential := range []string{"", "secret"} {
		if response := call("/api/v1/factory/runs", credential); response.Code != http.StatusUnauthorized {
			t.Fatalf("credential %q status = %d", credential, response.Code)
		}
	}
	list := call("/api/v1/factory/runs?limit=1", token)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"run_id":"run-1"`) || !strings.Contains(list.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	patch := call("/api/v1/factory/runs/run-1/patch", token)
	if patch.Code != http.StatusOK || patch.Body.String() != "<script>alert(1)</script>\n" || !strings.HasPrefix(patch.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("patch = %d %s", patch.Code, patch.Body.String())
	}
	if response := call("/api/v1/factory/runs/bad.id", token); response.Code != http.StatusNotFound {
		t.Fatalf("invalid ID status = %d", response.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", "bad.id")); !os.IsNotExist(err) {
		t.Fatalf("read endpoint created a run directory: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(runDir, factory.ArtifactPatch)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(runDir, factory.ArtifactPatch)); err != nil {
		t.Fatal(err)
	}
	if response := call("/api/v1/factory/runs/run-1/patch", token); response.Code == http.StatusOK || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("symlink escaped evidence root: %d %s", response.Code, response.Body.String())
	}
}

func TestFactoryReadRejectsWeakOrPublicTokens(t *testing.T) {
	server, web := newTestHTTPServer(t)
	web.Close()
	root := t.TempDir()
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.ConfigureFactoryRead(root, tokenPath); err == nil {
		t.Fatal("weak token accepted")
	}
	if err := os.WriteFile(tokenPath, []byte(strings.Repeat("a", 40)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tokenPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := server.ConfigureFactoryRead(root, tokenPath); err == nil {
		t.Fatal("world-readable token accepted")
	}
}

func TestFactoryReadIndexPaginatesAndRebuildsFromManifests(t *testing.T) {
	server, web := newTestHTTPServer(t)
	web.Close()
	root := t.TempDir()
	runsDir := filepath.Join(root, "runs")
	if err := os.Mkdir(runsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRun := func(id string, modified time.Time) {
		t.Helper()
		path := filepath.Join(runsDir, id)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "run.json"), []byte(`{"run_id":"`+id+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
	stamp := time.Now().Add(-time.Hour)
	writeRun("old", stamp)
	writeRun("new", stamp.Add(time.Minute))
	token := strings.Repeat("t", 40)
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := server.ConfigureFactoryRead(root, tokenPath); err != nil {
		t.Fatal(err)
	}
	page := func(cursor string) map[string]json.RawMessage {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/factory/runs?limit=1"+cursor, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("page: %d %s", response.Code, response.Body.String())
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := string(page("")["runs"]); !strings.Contains(got, `"run_id":"new"`) {
		t.Fatalf("first page = %s", got)
	}
	if got := string(page("&cursor=1")["runs"]); !strings.Contains(got, `"run_id":"old"`) {
		t.Fatalf("second page = %s", got)
	}
	writeRun("newest", stamp.Add(2*time.Minute))
	if err := os.Chtimes(runsDir, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := string(page("")["runs"]); !strings.Contains(got, `"run_id":"newest"`) {
		t.Fatalf("refreshed first page = %s", got)
	}
	if _, err := server.factoryRead.index.Exec(`DELETE FROM factory_entries`); err != nil {
		t.Fatal(err)
	}
	delete(server.factoryRead.indexedAt, "runs")
	if got := string(page("")["runs"]); !strings.Contains(got, `"run_id":"newest"`) {
		t.Fatalf("rebuilt first page = %s", got)
	}
}
