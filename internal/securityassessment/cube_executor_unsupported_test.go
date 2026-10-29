//go:build !linux

package securityassessment

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitkox/esf/internal/sandbox/cube"
	"github.com/mitkox/esf/internal/securityassessment/route"
)

func TestUnsupportedExecutorRefusesBeforeJournalOrAllocation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	archive := candidateArchive(t, map[string]string{"app.py": "pass"})
	candidate, err := candidateFromArchive(CandidateManifest{Schema: CandidateSchema, Entries: []CandidateEntry{{Path: "app.py", Kind: "file", Mode: 0644, Size: 4, SHA256: SHA256([]byte("pass"))}}}, archive, DefaultCandidatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "must-not-exist")
	executor := &CubeExecutor{config: CubeExecutorConfig{Cube: cube.Config{APIURL: server.URL}, PrivateJournalRoot: root, TargetEntrypoint: "app.py"}}
	_, err = executor.Assess(context.Background(), CubeAssessmentRequest{RunID: "unsupported-run", Profile: testProfile(), Candidate: candidate})
	if !errors.Is(err, route.ErrHostGuardUnsupported) || requests != 0 {
		t.Fatalf("unsupported backend made control-plane effects: requests=%d err=%v", requests, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("unsupported backend created private journal state")
	}
}
