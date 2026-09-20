package factory

import (
	"testing"
	"time"
)

// Conditions are the per-step lifecycle signal. These tests pin the properties
// the CLI and the manifest depend on: replacement rather than duplication,
// stable ordering, and honest counts.

func TestSetConditionReplacesInPlace(t *testing.T) {
	now := time.Now().UTC()
	conditions := []Condition{
		{Type: ConditionValidated, Status: ConditionTrue, LastTransitionTime: now},
		{Type: ConditionSandboxReady, Status: ConditionUnknown, LastTransitionTime: now},
	}
	conditions = SetCondition(conditions, Condition{
		Type: ConditionSandboxReady, Status: ConditionTrue, Reason: "Created", LastTransitionTime: now,
	})
	if len(conditions) != 2 {
		t.Fatalf("condition count = %d, want 2 (replacement, not append)", len(conditions))
	}
	if conditions[0].Type != ConditionValidated || conditions[1].Type != ConditionSandboxReady {
		t.Fatalf("order changed: %+v", conditions)
	}
	if conditions[1].Status != ConditionTrue || conditions[1].Reason != "Created" {
		t.Fatalf("replacement not applied: %+v", conditions[1])
	}
}

func TestSetConditionAppendsNewTypes(t *testing.T) {
	conditions := SetCondition(nil, Condition{Type: ConditionValidated, Status: ConditionTrue})
	conditions = SetCondition(conditions, Condition{Type: ConditionSandboxReady, Status: ConditionTrue})
	if len(conditions) != 2 {
		t.Fatalf("condition count = %d, want 2", len(conditions))
	}
	// A failed condition is never removed: it is exactly the fact an auditor
	// needs.
	conditions = SetCondition(conditions, Condition{Type: ConditionVerified, Status: ConditionFalse})
	verified, ok := ConditionFor(conditions, ConditionVerified)
	if !ok || verified.Status != ConditionFalse {
		t.Fatalf("verified condition = %+v, want a retained failure", verified)
	}
}

func TestConditionCountsAndSummary(t *testing.T) {
	conditions := []Condition{
		{Type: ConditionValidated, Status: ConditionTrue},
		{Type: ConditionSandboxReady, Status: ConditionTrue},
		{Type: ConditionVerified, Status: ConditionFalse},
		{Type: ConditionReviewPaused, Status: ConditionUnknown},
	}
	trueCount, falseCount, unknownCount := ConditionCounts(conditions)
	if trueCount != 2 || falseCount != 1 || unknownCount != 1 {
		t.Fatalf("counts = %d/%d/%d, want 2/1/1", trueCount, falseCount, unknownCount)
	}
	if got := ConditionSummary(conditions); got != "Validated=True SandboxReady=True Verified=False ReviewPaused=Unknown" {
		t.Fatalf("summary = %q", got)
	}
	if got := ConditionSummary(nil); got != "" {
		t.Fatalf("empty summary = %q, want empty", got)
	}
}

func TestRunStatePausedIsValid(t *testing.T) {
	if !StatePaused.Valid() {
		t.Fatal("PAUSED is not a valid run state")
	}
}
