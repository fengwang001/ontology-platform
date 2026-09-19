package rollback

import (
	"errors"
	"fmt"
	"testing"
)

func TestCompensationFailuresAggregatedAndRollbackContinues(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	ran := make([]int, 0)
	errStep2 := errors.New("comp-fail-2")
	errStep4 := errors.New("comp-fail-4")

	for i := 1; i <= 4; i++ {
		stepNo := i
		var comp Compensation = func() error {
			ran = append(ran, stepNo)
			return nil
		}
		if stepNo == 2 {
			comp = func() error {
				ran = append(ran, stepNo)
				return errStep2
			}
		}
		if stepNo == 4 {
			comp = func() error {
				ran = append(ran, stepNo)
				return errStep4
			}
		}
		if err := u.Step(
			func() error { _, err := store.Add("o", 1); return err },
			comp,
		); err != nil {
			t.Fatalf("step %d: %v", stepNo, err)
		}
	}

	err := u.Rollback()

	// Every compensation still ran, in reverse order.
	if got, want := ran, []int{4, 3, 2, 1}; !equalInts(got, want) {
		t.Fatalf("compensation order = %v, want %v", got, want)
	}
	if got := u.Trace(); !equalInts(got, []int{4, 3, 2, 1}) {
		t.Fatalf("trace = %v, want [4 3 2 1]", got)
	}

	var agg *AggregateError
	if !errors.As(err, &agg) {
		t.Fatalf("rollback err = %T %v, want *AggregateError", err, err)
	}
	if got := agg.Step(2); !errors.Is(got, errStep2) {
		t.Fatalf("step 2 reason = %v, want errStep2", got)
	}
	if got := agg.Step(4); !errors.Is(got, errStep4) {
		t.Fatalf("step 4 reason = %v, want errStep4", got)
	}
	if got := agg.Step(1); got != nil {
		t.Fatalf("step 1 should have succeeded, got %v", got)
	}
	failures := agg.Failures()
	if len(failures) != 2 || failures[0].Step != 2 || failures[1].Step != 4 {
		t.Fatalf("failures = %+v, want steps 2,4 ascending", failures)
	}

	// Pollution: point is the earliest failed compensation in forward order.
	if !store.Polluted() {
		t.Fatal("store must be polluted")
	}
	if got := store.PollutionStep(); got != 2 {
		t.Fatalf("pollution step = %d, want 2", got)
	}
}

func TestPollutedStoreRejectsWritesUntilReset(t *testing.T) {
	store := NewStore()
	store.MarkPolluted(5)
	store.MarkPolluted(2) // earlier point wins

	if _, err := store.Add("a", 1); !errors.Is(err, ErrPolluted) {
		t.Fatalf("Add = %v, want ErrPolluted", err)
	}
	if err := store.Set("a", 1); !errors.Is(err, ErrPolluted) {
		t.Fatalf("Set = %v, want ErrPolluted", err)
	}

	var pe *PollutionError
	_, addErr := store.Add("a", 1)
	if !errors.As(addErr, &pe) || pe.Step != 2 {
		t.Fatalf("want *PollutionError with step 2")
	}
	if !IsPolluted(fmt.Errorf("wrapped: %w", pe)) {
		t.Fatal("IsPolluted should traverse wraps")
	}

	// Reads still work.
	if got := store.Get("a"); got != 0 {
		t.Fatalf("Get on polluted store = %d, want 0", got)
	}

	// Only Reset clears pollution.
	store.Reset()
	if store.Polluted() || store.PollutionStep() != 0 {
		t.Fatal("Reset must clear pollution")
	}
	if _, err := store.Add("a", 1); err != nil {
		t.Fatalf("Add after Reset: %v", err)
	}
}

func TestFailedRollbackMarksPollutedEvenWithSingleFailure(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	wantErr := errors.New("only failure")
	_ = u.Step(
		func() error { return nil },
		func() error { return wantErr },
	)
	err := u.Rollback()
	var agg *AggregateError
	if !errors.As(err, &agg) || !errors.Is(agg.Step(1), wantErr) {
		t.Fatalf("err = %v", err)
	}
	if store.PollutionStep() != 1 {
		t.Fatalf("pollution step = %d, want 1", store.PollutionStep())
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
