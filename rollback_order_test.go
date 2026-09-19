package rollback

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// successfulSteps builds n steps that each add +1 to "obj" and compensate by
// adding -1. The compensation order is observed via trace (the package's own
// execution trace) and via an external recorder.
func successfulSteps(store *Store, n int, recorder *[]int) []StepDef {
	defs := make([]StepDef, 0, n)
	for i := 1; i <= n; i++ {
		stepNo := i
		defs = append(defs, StepDef{
			Action: func() error {
				_, err := store.Add("obj", 1)
				return err
			},
			Compensation: func() error {
				if recorder != nil {
					*recorder = append(*recorder, stepNo)
				}
				_, err := store.Add("obj", -1)
				return err
			},
		})
	}
	return defs
}

func TestReverseOrderAndExactlyOnce(t *testing.T) {
	store := NewStore()
	rec := make([]int, 0)
	u := NewUnit(store)

	for _, def := range successfulSteps(store, 3, &rec) {
		if err := u.Step(def.Action, def.Compensation); err != nil {
			t.Fatalf("unexpected step error: %v", err)
		}
	}

	// Step 4 fails: steps 1-3 compensate 3,2,1; step 4 never compensates.
	err := u.Step(
		func() error { return errors.New("boom at 4") },
		func() error {
			t.Fatal("failed step compensation must never run")
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected failure error")
	}

	want := []int{3, 2, 1}
	if got := u.Trace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
	if got := rec; !reflect.DeepEqual(got, want) {
		t.Fatalf("recorder = %v, want %v", got, want)
	}
	if got := store.Get("obj"); got != 0 {
		t.Fatalf("count after rollback = %d, want 0", got)
	}
}

func TestFirstStepFailsHasEmptyTrace(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	err := u.Step(
		func() error { return fmt.Errorf("immediate") },
		func() error {
			t.Fatal("failed first step must not be compensated")
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := u.Trace(); len(got) != 0 {
		t.Fatalf("trace = %v, want empty", got)
	}
}

func TestZeroStepRollbackIsNoOp(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	if err := u.Rollback(); err != nil {
		t.Fatalf("zero-step rollback = %v, want nil", err)
	}
	if got := u.Trace(); len(got) != 0 {
		t.Fatalf("trace = %v, want empty", got)
	}
	if store.Polluted() {
		t.Fatal("store must stay clean")
	}
}

func TestCommitThenRollbackRejected(t *testing.T) {
	store := NewStore()
	rec := make([]int, 0)
	u := NewUnit(store)

	if err := u.Run(successfulSteps(store, 3, &rec)...); err != nil {
		t.Fatalf("run: %v", err)
	}
	err := u.Rollback()
	if !errors.Is(err, ErrCommitted) {
		t.Fatalf("rollback after commit = %v, want ErrCommitted", err)
	}
	if got := u.Trace(); len(got) != 0 {
		t.Fatalf("committed unit ran compensations: %v", got)
	}
	if got := store.Get("obj"); got != 3 {
		t.Fatalf("count = %d, want 3 (committed changes kept)", got)
	}
}

func TestRollbackAfterRollbackIsNoReplay(t *testing.T) {
	store := NewStore()
	rec := make([]int, 0)
	u := NewUnit(store)

	for _, def := range successfulSteps(store, 3, &rec) {
		if err := u.Step(def.Action, def.Compensation); err != nil {
			t.Fatalf("step: %v", err)
		}
	}
	if err := u.Rollback(); err != nil {
		t.Fatalf("first rollback: %v", err)
	}
	if err := u.Rollback(); err != nil {
		t.Fatalf("second rollback = %v, must be idempotent (same nil result)", err)
	}
	if got := u.Trace(); !reflect.DeepEqual(got, []int{3, 2, 1}) {
		t.Fatalf("trace = %v", got)
	}
	// But registering/running a new step after rollback is rejected.
	if err := u.Step(func() error { return nil }, func() error { return nil }); !errors.Is(err, ErrRolledBack) {
		t.Fatalf("step after rollback = %v, want ErrRolledBack", err)
	}
}
