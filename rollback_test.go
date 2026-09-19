package ontology

import (
	"errors"
	"testing"
)

// Compensation runs in strict reverse order, exactly once per succeeded step;
// the failed step and steps after it are never compensated.
func TestReverseOrderAndExactlyOnce(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)
	var trace []int

	boom := errors.New("step 3 boom")
	for i := 1; i <= 5; i++ {
		i := i
		u.Add(func() error {
			if i == 3 {
				return boom
			}
			_, _ = store.Add("o", 1)
			return nil
		}, func() error {
			trace = append(trace, i)
			_, _ = store.Add("o", -1)
			return nil
		})
	}

	err := u.Run()
	if !errors.Is(err, boom) {
		t.Fatalf("want wrapped action error, got %v", err)
	}

	got := u.Trace()
	want := []int{2, 1}
	if len(got) != len(want) {
		t.Fatalf("trace = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace = %v, want %v", got, want)
		}
	}
	if len(trace) != len(want) {
		t.Fatalf("compensations ran %d times, want %d", len(trace), len(want))
	}
	if obj, _ := store.Get("o"); obj.Count != 0 {
		t.Fatalf("count after rollback = %d, want 0", obj.Count)
	}
}

// A failing compensation does not stop later compensations; all failures are
// aggregated and retrievable by step number.
func TestCompensationFailureContinuesAndAggregates(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	errComp1 := errors.New("comp 1 failed")
	errComp3 := errors.New("comp 3 failed")
	for i := 1; i <= 4; i++ {
		i := i
		u.Add(func() error { return nil }, func() error {
			switch i {
			case 1:
				return errComp1
			case 3:
				return errComp3
			default:
				return nil
			}
		})
	}

	err := u.Rollback()
	var agg *AggregateError
	if !errors.As(err, &agg) {
		t.Fatalf("want *AggregateError, got %v", err)
	}
	if got, ok := agg.FailureFor(3); !ok || !errors.Is(got, errComp3) {
		t.Fatalf("step 3 failure missing or wrong: %v %v", got, ok)
	}
	if got, ok := agg.FailureFor(1); !ok || !errors.Is(got, errComp1) {
		t.Fatalf("step 1 failure missing or wrong: %v %v", got, ok)
	}
	if _, ok := agg.FailureFor(2); ok {
		t.Fatalf("step 2 must not appear as failure")
	}
	if len(agg.Failures()) != 2 {
		t.Fatalf("want 2 failures, got %d", len(agg.Failures()))
	}
	if got := u.Trace(); len(got) != 4 {
		t.Fatalf("all compensations must run, trace = %v", got)
	}
}

// A panicking compensation is converted to a step failure and later
// compensations still execute.
func TestPanicCompensationIsCaught(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)
	laterRan := false

	for i := 1; i <= 3; i++ {
		i := i
		u.Add(func() error { return nil }, func() error {
			if i == 2 {
				panic("kaboom")
			}
			if i == 1 {
				laterRan = true
			}
			return nil
		})
	}

	err := u.Rollback()
	var agg *AggregateError
	if !errors.As(err, &agg) {
		t.Fatalf("want *AggregateError, got %v", err)
	}
	perr, ok := agg.FailureFor(2)
	if !ok {
		t.Fatalf("step 2 missing from aggregate")
	}
	pe, isPanic := AsPanic(perr)
	if !isPanic || pe.Value != "kaboom" {
		t.Fatalf("want PanicError with value, got %#v", perr)
	}
	if !laterRan {
		t.Fatalf("compensation after panic (step 1) must still run")
	}
	if got := u.Trace(); len(got) != 3 {
		t.Fatalf("trace = %v, want all 3 steps", got)
	}
}
