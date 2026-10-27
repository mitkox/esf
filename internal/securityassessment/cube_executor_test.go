package securityassessment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitkox/esf/internal/sandbox/cube"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

func TestCubeFailureCapturesContextBeforeDetachedCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	failure := preserveCubeFailureContext(ctx, errors.New("unrelated startup error"))
	// The injection fires while detached cleanup runs, after failure capture.
	cancel()
	if errors.Is(failure, context.Canceled) {
		t.Fatal("cleanup-time cancellation recategorized an earlier failure")
	}
	if got := preserveCubeFailureContext(ctx, errors.New("interrupted request")); !errors.Is(got, context.Canceled) {
		t.Fatal("actual cancellation cause discarded")
	}
	if got := preserveCubeFailureContext(ctx, nil); got != nil {
		t.Fatal("successful operation recategorized as canceled")
	}
}

func TestCubeExecutorRejectsUnpinnedDriverBeforeAllocation(t *testing.T) {
	dir := t.TempDir()
	driver := filepath.Join(dir, "driver")
	if err := os.WriteFile(driver, []byte("driver-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	config := CubeExecutorConfig{Cube: cube.Config{APIURL: "http://127.0.0.1:4000", TemplateID: "tpl-target", ProxyNodeIP: "127.0.0.1", ProxyPortHTTP: 9443, ProxyScheme: "https", Version: "0.7.3"},
		GuardSocket: filepath.Join(dir, "guard.sock"), DriverBinaryPath: driver, DriverSHA256: SHA256([]byte("different")), TargetEntrypoint: "server.py", PrivateJournalRoot: filepath.Join(dir, "private"), RuntimeInventorySHA256: strings.Repeat("a", 64)}
	if _, err := NewCubeExecutor(config); err == nil {
		t.Fatal("accepted mutable or mismatched driver")
	}
	config.DriverSHA256 = SHA256([]byte("driver-bytes"))
	if _, err := NewCubeExecutor(config); err != nil {
		t.Fatal(err)
	}
	config.TargetEntrypoint = "../outside.py"
	if _, err := NewCubeExecutor(config); err == nil {
		t.Fatal("accepted target path escape")
	}
}

func TestCubeFileTransferRejectsOversizeAndRedirectWithoutFollowing(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", "/secret")
			w.WriteHeader(302)
			return
		}
		if r.URL.Path == "/secret" {
			t.Error("followed file transport redirect")
		}
		w.Write(bytes.Repeat([]byte("x"), 1024))
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	if _, err := readBoundedHTTP(context.Background(), client, server.URL+"/files", nil, 16); err == nil {
		t.Fatal("unbounded target file transferred")
	}
	if _, err := readBoundedHTTP(context.Background(), client, server.URL+"/redirect", nil, 1024); err == nil {
		t.Fatal("file transport accepted redirect")
	}
	if requests != 2 {
		t.Fatalf("unexpected transport requests: %d", requests)
	}
}

func TestCubeCleanupAttemptsAllOwnedDeletesAndPreservesUnknownAllocation(t *testing.T) {
	owner := "qualification-0123456789abcdef0123456789abcdef"
	deleted := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/sandboxes" {
			items := []cubesandbox.SandboxInfo{{SandboxID: "unrelated"}}
			for _, id := range []string{"a", "b"} {
				if !deleted[id] {
					items = append(items, cubesandbox.SandboxInfo{SandboxID: id, Metadata: map[string]string{"esf.security.owner": owner}})
				}
			}
			json.NewEncoder(w).Encode(items)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/sandboxes/a" {
			w.WriteHeader(503)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/sandboxes/b" {
			deleted["b"] = true
			w.WriteHeader(204)
			return
		}
		t.Errorf("unrelated resource mutated: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}))
	defer server.Close()
	client := cubesandbox.NewClient(cubesandbox.Config{APIURL: server.URL})
	defer client.Close()
	cleanup, err := cleanupCubeOwned(context.Background(), client, server.Client(), cube.Config{APIURL: server.URL}, owner, []string{"a", "b"}, false)
	if err == nil || cleanup.Status == Complete || !deleted["b"] || len(cleanup.RemainingIDs) != 1 || cleanup.RemainingIDs[0] != "a" {
		t.Fatalf("unsafe cleanup: %+v, %v", cleanup, err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode([]cubesandbox.SandboxInfo{}) }))
	defer empty.Close()
	other := cubesandbox.NewClient(cubesandbox.Config{APIURL: empty.URL})
	defer other.Close()
	cleanup, err = cleanupCubeOwned(context.Background(), other, empty.Client(), cube.Config{APIURL: empty.URL}, owner, nil, true)
	if err == nil || cleanup.Status == Complete || !cleanup.PendingAllocation {
		t.Fatal("empty inventory erased unknown allocation")
	}
}

func TestCubeExecutorRevalidatesArchiveBeforeRemoteSideEffects(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	archive := candidateArchive(t, map[string]string{"app.py": "dirty"})
	candidate, err := candidateFromArchive(CandidateManifest{Schema: CandidateSchema, Entries: []CandidateEntry{{Path: "app.py", Kind: "file", Mode: 0644, Size: 5, SHA256: SHA256([]byte("dirty"))}}}, archive, DefaultCandidatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	// Even an otherwise valid candidate whose archive changes after capture must
	// fail before journal creation, template lookup, or VM allocation.
	candidate.Archive = append(append([]byte(nil), candidate.Archive...), 1)
	dir := filepath.Join(t.TempDir(), "must-not-exist")
	executor := &CubeExecutor{config: CubeExecutorConfig{Cube: cube.Config{APIURL: server.URL}, PrivateJournalRoot: dir, TargetEntrypoint: "app.py"}}
	if _, err := executor.Assess(context.Background(), CubeAssessmentRequest{RunID: "candidate-run", Profile: testProfile(), Candidate: candidate}); err == nil {
		t.Fatal("changed archive accepted")
	}
	if requests != 0 {
		t.Fatalf("remote side effects preceded candidate validation: %d", requests)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("journal created before candidate validation")
	}
}

func TestCubeCleanupCannotEraseKnownVMWithChangedOwnerMetadata(t *testing.T) {
	owner := "qualification-0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("mutated VM after ownership metadata changed")
		}
		json.NewEncoder(w).Encode([]cubesandbox.SandboxInfo{{SandboxID: "known-vm", Metadata: map[string]string{"esf.security.owner": "different-owner"}}})
	}))
	defer server.Close()
	client := cubesandbox.NewClient(cubesandbox.Config{APIURL: server.URL})
	defer client.Close()
	cleanup, err := cleanupCubeOwned(context.Background(), client, server.Client(), cube.Config{APIURL: server.URL}, owner, []string{"known-vm"}, false)
	if err == nil || cleanup.Status != Incomplete || len(cleanup.RemainingIDs) != 1 || cleanup.RemainingIDs[0] != "known-vm" {
		t.Fatalf("known VM disappeared from cleanup accounting: %+v, %v", cleanup, err)
	}
}

func TestCubeRuntimeTemplateRequiresLiveImmutableImageOrSinglePinnedRootFS(t *testing.T) {
	rootfs := strings.Repeat("d", 64)
	pin := testRuntimePin()
	pin.Assets = map[string]string{"cube-rootfs-sha256": rootfs}
	container := func(hash string) any {
		return map[string]any{"image": map[string]any{"annotations": map[string]string{"cube.master.rootfs.artifact.sha256": hash}}}
	}
	for _, tc := range []struct {
		name   string
		change func(map[string]any, *RuntimePin)
		want   bool
	}{
		{"single pinned ext4 rootfs", func(map[string]any, *RuntimePin) {}, true},
		{"no rootfs annotation", func(m map[string]any, _ *RuntimePin) {
			m["createRequest"] = map[string]any{"containers": []any{container("")}}
		}, false},
		{"mismatched rootfs", func(m map[string]any, _ *RuntimePin) {
			m["createRequest"] = map[string]any{"containers": []any{container(strings.Repeat("e", 64))}}
		}, false},
		{"multiple containers", func(m map[string]any, _ *RuntimePin) {
			m["createRequest"] = map[string]any{"containers": []any{container(rootfs), container(rootfs)}}
		}, false},
		{"missing create request", func(m map[string]any, _ *RuntimePin) { delete(m, "createRequest") }, false},
		{"unpinned rootfs", func(_ map[string]any, p *RuntimePin) { p.Assets = nil }, false},
		{"malformed rootfs pin", func(_ map[string]any, p *RuntimePin) { p.Assets["cube-rootfs-sha256"] = "not-a-hash" }, false},
		{"wrong template", func(m map[string]any, _ *RuntimePin) { m["templateID"] = "different" }, false},
		{"not ready", func(m map[string]any, _ *RuntimePin) { m["status"] = "BUILDING" }, false},
		{"pinned OCI path", func(m map[string]any, p *RuntimePin) {
			m["imageInfo"] = "registry.test/approved@" + p.ImageDigest
			delete(m, "createRequest")
			p.Assets = nil
		}, true},
		{"mutable OCI path", func(m map[string]any, p *RuntimePin) {
			m["imageInfo"] = "registry.test/approved:latest"
			delete(m, "createRequest")
			p.Assets = nil
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			localPin := pin
			localPin.Assets = map[string]string{"cube-rootfs-sha256": rootfs}
			metadata := map[string]any{"templateID": pin.TemplateID, "status": "READY", "createRequest": map[string]any{"containers": []any{container(rootfs)}}}
			tc.change(metadata, &localPin)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/templates/"+pin.TemplateID || r.Header.Get("Authorization") != "Bearer controller-only-key" {
					t.Error("template metadata request lost trusted identity or authentication")
				}
				json.NewEncoder(w).Encode(metadata)
			}))
			defer server.Close()
			err := verifyCubeRuntimeTemplate(context.Background(), server.Client(), cube.Config{APIURL: server.URL, APIKey: "controller-only-key"}, localPin)
			if (err == nil) != tc.want {
				t.Fatalf("pin verification error = %v, want accepted=%v", err, tc.want)
			}
		})
	}
}
