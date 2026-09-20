package sandbox

import (
	"context"
	"errors"
	"testing"
)

// The lifecycle interfaces (suspend, resume, preview, state) are optional: the
// factory detects them by type assertion and records SKIPPED when they are
// absent. These tests pin the fake's behaviour, because the workflow tests rely
// on it to prove the real suspend/resume path.

func TestFakeLifecycleSuspendResumeIsIdempotent(t *testing.T) {
	fake := NewFake()
	sb, err := fake.Create(context.Background(), Spec{Template: "tpl"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx := context.Background()

	if err := fake.Suspend(ctx, sb.ID()); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if !fake.Suspended(sb.ID()) {
		t.Fatal("sandbox is not marked suspended")
	}
	// Idempotent: a retried activity may suspend twice.
	if err := fake.Suspend(ctx, sb.ID()); err != nil {
		t.Fatalf("second Suspend: %v", err)
	}
	state, err := fake.SandboxState(ctx, sb.ID())
	if err != nil || state != StatePaused {
		t.Fatalf("state = %q, %v; want paused", state, err)
	}

	if err := fake.Resume(ctx, sb.ID()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if fake.Suspended(sb.ID()) {
		t.Fatal("sandbox is still marked suspended after resume")
	}
	if err := fake.Resume(ctx, sb.ID()); err != nil {
		t.Fatalf("second Resume: %v", err)
	}
	state, err = fake.SandboxState(ctx, sb.ID())
	if err != nil || state != StateRunning {
		t.Fatalf("state = %q, %v; want running", state, err)
	}
	if len(fake.SuspendCalls()) != 2 || len(fake.ResumeCalls()) != 2 {
		t.Fatalf("calls = %v/%v, want both recorded", fake.SuspendCalls(), fake.ResumeCalls())
	}
}

func TestFakeLifecycleUnknownSandboxIsRejected(t *testing.T) {
	fake := NewFake()
	ctx := context.Background()
	if err := fake.Suspend(ctx, "missing"); err == nil {
		t.Fatal("Suspend accepted an unknown sandbox")
	}
	if err := fake.Resume(ctx, "missing"); err == nil {
		t.Fatal("Resume accepted an unknown sandbox")
	}
	if _, err := fake.SandboxState(ctx, "missing"); err == nil {
		t.Fatal("SandboxState accepted an unknown sandbox")
	}
	if _, err := fake.PreviewURL(ctx, "missing", 3000); err == nil {
		t.Fatal("PreviewURL accepted an unknown sandbox")
	}
}

func TestFakeLifecycleErrorsAreInjectable(t *testing.T) {
	fake := NewFake()
	sb, err := fake.Create(context.Background(), Spec{Template: "tpl"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx := context.Background()
	wantErr := errors.New("provider unavailable")
	fake.SetSuspendErr(wantErr)
	if err := fake.Suspend(ctx, sb.ID()); !errors.Is(err, wantErr) {
		t.Fatalf("Suspend error = %v, want %v", err, wantErr)
	}
	fake.SetSuspendErr(nil)
	fake.SetResumeErr(wantErr)
	if err := fake.Resume(ctx, sb.ID()); !errors.Is(err, wantErr) {
		t.Fatalf("Resume error = %v, want %v", err, wantErr)
	}
}

func TestFakePreviewURLIsDeterministic(t *testing.T) {
	fake := NewFake()
	sb, err := fake.Create(context.Background(), Spec{Template: "tpl"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := fake.PreviewURL(context.Background(), sb.ID(), 3000)
	if err != nil {
		t.Fatalf("PreviewURL: %v", err)
	}
	second, _ := fake.PreviewURL(context.Background(), sb.ID(), 3000)
	if first != second {
		t.Fatalf("PreviewURL is not deterministic: %q vs %q", first, second)
	}
}

// TestFakeCapabilitiesAdvertiseLifecycle proves the workflow tests exercise the
// suspend path rather than the degraded one.
func TestFakeCapabilitiesAdvertiseLifecycle(t *testing.T) {
	caps := NewFake().Capabilities()
	if !caps.Suspend || !caps.Resume || !caps.Preview {
		t.Fatalf("fake capabilities = %+v, want suspend/resume/preview advertised", caps)
	}
	// The fake must satisfy the optional interfaces it advertises.
	var provider Provider = NewFake()
	if _, ok := provider.(Suspender); !ok {
		t.Fatal("fake does not implement Suspender")
	}
	if _, ok := provider.(Previewer); !ok {
		t.Fatal("fake does not implement Previewer")
	}
	if _, ok := provider.(Stater); !ok {
		t.Fatal("fake does not implement Stater")
	}
}
