package ontology

import (
	"errors"
	"testing"
)

// A zero-step unit: rollback is a no-op returning nil.
func TestZeroStepRollbackIsNoOp(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	if err := u.Rollback(); err != nil {
		t.Fatalf("zero-step rollback err = %v, want nil", err)
	}
	if tr := u.Trace(); len(tr) != 0 {
		t.Fatalf("trace = %v, want empty", tr)
	}
	if tainted, _ := store.Tainted(); tainted {
		t.Fatalf("store must stay clean")
	}

	// A zero-step Run commits successfully too.
	u2 := NewUnit(NewStore())
	if err := u2.Run(); err != nil {
		t.Fatalf("zero-step Run err = %v, want nil", err)
	}
}

// Failing on the first step: no compensation trace at all.
func TestFirstStepFailureHasEmptyTrace(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)
	called := false
	boom := errors.New("first step fails")

	u.Add(func() error { return boom }, func() error {
		called = true
		return nil
	})

	err := u.Run()
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if tr := u.Trace(); len(tr) != 0 {
		t.Fatalf("trace = %v, want empty", tr)
	}
	if called {
		t.Fatalf("failed step's own compensation must not run")
	}
}

// After successful commit, rollback is rejected with a detectable error and
// compensations never run.
func TestRollbackRejectedAfterCommit(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)
	called := false

	u.Add(func() error { return nil }, func() error {
		called = true
		return nil
	})

	if err := u.Run(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	err := u.Rollback()
	var rejected *RollbackRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("want *RollbackRejectedError, got %v", err)
	}
	if called {
		t.Fatalf("compensation must not run after commit")
	}
	if tr := u.Trace(); len(tr) != 0 {
		t.Fatalf("trace = %v, want empty", tr)
	}
}

// An all-success run commits and performs no compensations.
func TestAllSuccessNoCompensation(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	for i := 0; i < 3; i++ {
		u.Add(func() error { return nil }, func() error {
			t.Fatal("compensation must never run on success")
			return nil
		})
	}
	if err := u.Run(); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if tr := u.Trace(); len(tr) != 0 {
		t.Fatalf("trace = %v, want empty", tr)
	}
}

// A second rollback after completion returns the same result without executing
// anything again.
func TestRollbackIsIdempotentAcrossCalls(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	u.Add(func() error { return nil }, func() error { return nil })

	if err := u.Rollback(); err != nil {
		t.Fatalf("first rollback = %v", err)
	}
	if err := u.Rollback(); err != nil {
		t.Fatalf("second rollback = %v, want nil", err)
	}
	if tr := u.Trace(); len(tr) != 1 {
		t.Fatalf("trace = %v, want exactly [1]", tr)
	}
}
