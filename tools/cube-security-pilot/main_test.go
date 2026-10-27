package main

import (
	"context"
	"errors"
	"testing"
)

func TestInjectedFaultRequiresTypedOriginalFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	original := errors.New("unrelated startup error")
	// A timer firing during cleanup changes the live context, not this cause.
	cancel()
	if ctx.Err() == nil {
		t.Fatal("test did not simulate late cancellation")
	}
	if injectedFailure("cancel", original, nil) || injectedFailure("timeout", original, nil) {
		t.Fatal("late timer masked unrelated failure")
	}
	if !injectedFailure("cancel", errors.Join(original, context.Canceled), nil) {
		t.Fatal("typed cancellation lost")
	}
	if !injectedFailure("timeout", errors.Join(original, context.DeadlineExceeded), nil) {
		t.Fatal("typed deadline lost")
	}
	if injectedFailure("cancel", context.DeadlineExceeded, nil) || injectedFailure("timeout", context.Canceled, nil) {
		t.Fatal("wrong injected fault matched")
	}
	if injectedFailure("cancel", context.Canceled, context.Canceled) {
		t.Fatal("operator cancellation treated as injected fault")
	}
}
