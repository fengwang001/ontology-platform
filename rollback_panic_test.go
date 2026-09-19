package rollback

import (
	"errors"
	"testing"
)

func TestCompensationPanicCaughtAndDoesNotStopOthers(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	ran := make([]int, 0)
	for i := 1; i <= 4; i++ {
		stepNo := i
		comp := func() error {
			ran = append(ran, stepNo)
			return nil
		}
		if stepNo == 3 {
			comp = func() error {
				ran = append(ran, stepNo)
				panic("kaboom-3")
			}
		}
		if err := u.Step(func() error { return nil }, comp); err != nil {
			t.Fatalf("step %d: %v", stepNo, err)
		}
	}

	err := u.Rollback()

	// Panic at 3 must not stop compensations 2 and 1 from running.
	if got := ran; !equalInts(got, []int{4, 3, 2, 1}) {
		t.Fatalf("compensation order = %v, want 4 3 2 1", got)
	}
	if got := u.Trace(); !equalInts(got, []int{4, 3, 2, 1}) {
		t.Fatalf("trace = %v, want 4 3 2 1", got)
	}

	var agg *AggregateError
	if !errors.As(err, &agg) {
		t.Fatalf("err = %v, want AggregateError", err)
	}

	var panicErr *CompensationPanic
	if !errors.As(agg.Step(3), &panicErr) {
		t.Fatalf("step 3 = %v, want *CompensationPanic", agg.Step(3))
	}
	if panicErr.Step != 3 || panicErr.Value != "kaboom-3" {
		t.Fatalf("panic = %+v, want step 3 / value kaboom-3", panicErr)
	}
	if agg.Step(1) != nil || agg.Step(2) != nil || agg.Step(4) != nil {
		t.Fatalf("only step 3 may fail, got %v", agg.Failures())
	}

	if !store.Polluted() || store.PollutionStep() != 3 {
		t.Fatalf("pollution = %v step %d", store.Polluted(), store.PollutionStep())
	}
}

func TestCompensationPanicWithNonStringValue(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	_ = u.Step(
		func() error { return nil },
		func() error { panic(42) },
	)

	var agg *AggregateError
	if !errors.As(u.Rollback(), &agg) {
		t.Fatal("want aggregate")
	}
	var panicErr *CompensationPanic
	if !errors.As(agg.Step(1), &panicErr) || panicErr.Value != 42 {
		t.Fatalf("panic value = %v, want 42", agg.Step(1))
	}
}

func TestMixedPanicAndErrorAggregatedTogether(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	plain := errors.New("plain-fail")
	comps := []Compensation{
		func() error { return nil },       // step 1 ok
		func() error { panic("panic-2") }, // step 2 panic
		func() error { return plain },     // step 3 error
	}
	for _, comp := range comps {
		if err := u.Step(func() error { return nil }, comp); err != nil {
			t.Fatalf("step: %v", err)
		}
	}

	var agg *AggregateError
	if !errors.As(u.Rollback(), &agg) {
		t.Fatal("want aggregate")
	}
	failures := agg.Failures()
	if len(failures) != 2 || failures[0].Step != 2 || failures[1].Step != 3 {
		t.Fatalf("failures = %+v, want steps 2,3", failures)
	}
	if store.PollutionStep() != 2 {
		t.Fatalf("pollution step = %d, want 2", store.PollutionStep())
	}
}
