package rollback

import (
	"errors"
	"testing"
)

func TestStoreAddSetGetAndReset(t *testing.T) {
	store := NewStore()

	got, err := store.Add("a", 5)
	if err != nil || got != 5 {
		t.Fatalf("Add = (%d,%v), want 5,nil", got, err)
	}
	got, err = store.Add("a", -2)
	if err != nil || got != 3 {
		t.Fatalf("Add = (%d,%v), want 3,nil", got, err)
	}
	if err := store.Set("b", 9); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if store.Get("a") != 3 || store.Get("b") != 9 || store.Get("missing") != 0 {
		t.Fatalf("gets = %d %d %d", store.Get("a"), store.Get("b"), store.Get("missing"))
	}

	store.Reset()
	if store.Get("a") != 0 || store.Get("b") != 0 {
		t.Fatal("Reset must drop objects")
	}
	if store.Polluted() {
		t.Fatal("Reset must clear pollution")
	}
}

func TestMidwayFailureRestoresCounters(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	add := func(id string, delta int) (Action, Compensation) {
		return func() error {
				_, err := store.Add(id, delta)
				return err
			},
			func() error {
				_, err := store.Add(id, -delta)
				return err
			}
	}

	for _, step := range []struct {
		id    string
		delta int
	}{
		{"a", 2},
		{"b", 3},
		{"c", 5},
	} {
		action, comp := add(step.id, step.delta)
		if err := u.Step(action, comp); err != nil {
			t.Fatalf("step: %v", err)
		}
	}

	// Fourth step fails after mutating nothing; earlier steps roll back.
	before := map[string]int{"a": store.Get("a"), "b": store.Get("b"), "c": store.Get("c")}
	err := u.Step(
		func() error { return errors.New("fourth failed") },
		func() error { return nil },
	)
	if err == nil {
		t.Fatal("expected failure")
	}

	for id, value := range before {
		// Counters after rollback must equal the pre-unit baseline (0 here),
		// regardless of their values right before the failure.
		_ = value
		if got := store.Get(id); got != 0 {
			t.Fatalf("object %s = %d after rollback, want 0", id, got)
		}
	}

	var stepErr *StepFailure
	if !errors.As(err, &stepErr) || stepErr.Step != 4 {
		t.Fatalf("err = %v, want StepFailure at 4", err)
	}
}

func TestSuccessfulRunKeepsCountersAndCommit(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	err := u.Run(
		StepDef{
			Action:       func() error { _, e := store.Add("x", 1); return e },
			Compensation: func() error { _, e := store.Add("x", -1); return e },
		},
		StepDef{
			Action:       func() error { _, e := store.Add("x", 2); return e },
			Compensation: func() error { _, e := store.Add("x", -2); return e },
		},
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if store.Get("x") != 3 {
		t.Fatalf("count = %d, want 3", store.Get("x"))
	}
	if err := u.Commit(); !errors.Is(err, ErrCommitted) {
		t.Fatalf("double commit = %v, want ErrCommitted", err)
	}
}
