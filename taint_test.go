package ontology

import (
	"errors"
	"testing"
)

// After a compensation failure the store is tainted at the earliest failing
// step, all writes are refused with a detectable error, and only Reset clears it.
func TestTaintWritesRejectedAndReset(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	for i := 1; i <= 4; i++ {
		i := i
		u.Add(func() error { return nil }, func() error {
			if i == 1 || i == 3 {
				return errors.New("comp fail")
			}
			return nil
		})
	}

	if err := u.Rollback(); err == nil {
		t.Fatalf("expected aggregate error")
	}

	tainted, step := store.Tainted()
	if !tainted || step != 1 {
		t.Fatalf("tainted=%v step=%d, want tainted at step 1", tainted, step)
	}

	err := store.Put(Object{ID: "x", Count: 1})
	var te *TaintedError
	if !errors.As(err, &te) {
		t.Fatalf("Put want *TaintedError, got %v", err)
	}
	if te.FirstTaintedStep != 1 {
		t.Fatalf("taint point = %d, want 1", te.FirstTaintedStep)
	}
	if _, err := store.Add("x", 1); !errors.Is(err, &TaintedError{}) {
		t.Fatalf("Add want taint error, got %v", err)
	}

	store.Reset()
	if tainted, _ := store.Tainted(); tainted {
		t.Fatalf("Reset must clear taint")
	}
	if err := store.Put(Object{ID: "x", Count: 1}); err != nil {
		t.Fatalf("write after Reset must succeed, got %v", err)
	}
}

// The earliest failing (smallest step number) compensation defines the taint
// point even when a larger-numbered step fails first in reverse order.
func TestTaintPointIsEarliestStep(t *testing.T) {
	store := NewStore()
	u := NewUnit(store)

	for i := 1; i <= 5; i++ {
		i := i
		u.Add(func() error { return nil }, func() error {
			if i == 2 || i == 4 {
				return errors.New("bad")
			}
			return nil
		})
	}

	_ = u.Rollback()
	tainted, step := store.Tainted()
	if !tainted || step != 2 {
		t.Fatalf("taint point = %d, want 2 (earliest failed step)", step)
	}
}
