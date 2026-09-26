package factory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mitkox/esf/internal/artifacts"
	"github.com/mitkox/esf/internal/sandbox"
)

// Exercise the public workflow and canonical read APIs with no registry,
// quality socket, database, provider or admission token on every supported OS.
func TestFactoryWorksWithoutQMS(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := Default()
	if cfg.Quality.Enabled() {
		t.Fatal("QMS must be disabled by default")
	}
	cfg.Storage.DataDir = root
	if err := cfg.validateQuality(); err != nil {
		t.Fatal(err)
	}
	req := baseRequest(t)
	if required, err := cfg.QualityRequired(req); err != nil || required {
		t.Fatalf("standard request requires QMS: %v, %v", required, err)
	}
	store, err := artifacts.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runtime{Config: cfg, Artifacts: store}
	if err = r.openQuality(); err != nil || r.Quality != nil {
		t.Fatalf("disabled runtime opened quality service: %v", err)
	}
	fake := sandbox.NewFake()
	fake.ExecuteFunc = wrapWithBuildSuccess(gitAwareExecute("diff --git a/greeting.py b/greeting.py"))
	manifest, err := runWorkflowOpts(t, fake, req, workflowRunOptions{artifactDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FactoryResult != StateSucceeded || !manifest.CleanupResult.Verified || manifest.Quality != nil || len(fake.Live()) != 0 {
		t.Fatalf("standard workflow failed or claimed quality approval: %+v", manifest)
	}
	saved, err := r.ReadRunManifest(ctx, req.RunID)
	if err != nil || saved.FactoryResult != StateSucceeded || saved.Quality != nil {
		t.Fatalf("standard manifest read: %+v, %v", saved, err)
	}
	ids, err := r.RunIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != req.RunID {
		t.Fatalf("standard run listing: %v, %v", ids, err)
	}
	for _, path := range []string{filepath.Join(root, "quality"), cfg.QualitySocket()} {
		if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("disabled QMS created %s: %v", path, err)
		}
	}
}

func TestDisabledQMSCannotAcceptControlledAdmission(t *testing.T) {
	a := &Activities{cfg: Default()}
	_, err := a.QualityRoute(context.Background(), QualityRouteInput{Request: RunRequest{RunID: "controlled", AdmissionID: "admitted-elsewhere"}})
	if err == nil {
		t.Fatal("controlled admission silently downgraded to standard execution")
	}
	cfg := Default()
	cfg.Scopes[DefaultScope] = ScopeConfig{QualityPolicies: []string{"required"}}
	if err = cfg.validateQuality(); err == nil {
		t.Fatal("required scope policy silently disabled")
	}
}
