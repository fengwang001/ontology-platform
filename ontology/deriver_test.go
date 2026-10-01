package ontology

import (
	"errors"
	"sync"
	"testing"
)

func addForTest(t *testing.T, d *Deriver, pred Predicate) {
	t.Helper()
	err := d.Add(pred)
	t.Logf("Add(%v) => %v", pred, err)
	if err != nil {
		t.Fatalf("Add(%v): %v", pred, err)
	}
}

func assertRange(t *testing.T, d *Deriver, column string, wantLo, wantHi int64) {
	t.Helper()
	lo, hi, err := d.Range(column)
	t.Logf("Range(%q) => lo=%d hi=%d err=%v (want lo=%d hi=%d)", column, lo, hi, err, wantLo, wantHi)
	if err != nil {
		t.Fatalf("Range(%q): %v", column, err)
	}
	if lo != wantLo || hi != wantHi {
		t.Fatalf("Range(%q) = [%d,%d], want [%d,%d]", column, lo, hi, wantLo, wantHi)
	}
}

func assertPinned(t *testing.T, d *Deriver, column string, wantValue int64, wantPinned bool) {
	t.Helper()
	value, pinned, err := d.Pinned(column)
	t.Logf("Pinned(%q) => value=%d pinned=%t err=%v (want value=%d pinned=%t)", column, value, pinned, err, wantValue, wantPinned)
	if err != nil {
		t.Fatalf("Pinned(%q): %v", column, err)
	}
	if value != wantValue || pinned != wantPinned {
		t.Fatalf("Pinned(%q) = (%d,%t), want (%d,%t)", column, value, pinned, wantValue, wantPinned)
	}
}

func assertExcluded(t *testing.T, d *Deriver, column string, want []int64) {
	t.Helper()
	got, err := d.Excluded(column)
	t.Logf("Excluded(%q) => %v err=%v (want %v)", column, got, err, want)
	if err != nil {
		t.Fatalf("Excluded(%q): %v", column, err)
	}
	if len(got) != len(want) {
		t.Fatalf("Excluded(%q) = %v, want %v", column, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Excluded(%q) = %v, want %v", column, got, want)
		}
	}
}

func TestCascadeNormalizationPinsAndRejectsFinalExclusion(t *testing.T) {
	d := NewDeriver()
	addForTest(t, d, NewCmp("x", OpGreaterEqual, 3))
	addForTest(t, d, NewCmp("x", OpLessEqual, 5))
	addForTest(t, d, NewCmp("x", OpNotEqual, 3))
	assertRange(t, d, "x", 4, 5)
	addForTest(t, d, NewCmp("x", OpNotEqual, 4))
	assertRange(t, d, "x", 5, 5)
	assertPinned(t, d, "x", 5, true)
	assertExcluded(t, d, "x", nil)

	err := d.Add(NewCmp("x", OpNotEqual, 5))
	t.Logf("Add x != 5 after pinning => %v (want %v)", err, ErrContradiction)
	if !errors.Is(err, ErrContradiction) {
		t.Fatalf("excluding pinned value: error %v, want %v", err, ErrContradiction)
	}
	assertRange(t, d, "x", 5, 5)
	assertPinned(t, d, "x", 5, true)
}

func TestStrictBoundsAtInt64Limits(t *testing.T) {
	for _, tc := range []struct {
		name string
		pred Predicate
	}{
		{"less than MinInt64", NewCmp("x", OpLess, MinInt64)},
		{"greater than MaxInt64", NewCmp("x", OpGreater, MaxInt64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDeriver()
			err := d.Add(tc.pred)
			t.Logf("Add(%v) => %v (want %v); rejected input must not register x", tc.pred, err, ErrContradiction)
			if !errors.Is(err, ErrContradiction) {
				t.Fatalf("error %v, want contradiction", err)
			}
			_, _, err = d.Range("x")
			if !errors.Is(err, ErrUnknownColumn) {
				t.Fatalf("Range(x) after rejection: %v, want unknown column", err)
			}
		})
	}
}

func TestEqMergesIntervalsAndExcludedValues(t *testing.T) {
	d := NewDeriver()
	addForTest(t, d, NewCmp("a", OpLessEqual, 10))
	addForTest(t, d, NewCmp("a", OpNotEqual, 5))
	addForTest(t, d, NewCmp("b", OpGreaterEqual, 7))
	addForTest(t, d, NewCmp("b", OpNotEqual, 9))
	addForTest(t, d, NewEq("a", "b"))

	assertRange(t, d, "a", 7, 10)
	assertRange(t, d, "b", 7, 10)
	assertExcluded(t, d, "a", []int64{9})
	assertExcluded(t, d, "b", []int64{9})
	same, err := d.Same("a", "b")
	t.Logf("Same(a,b) => %t err=%v (want true)", same, err)
	if err != nil || !same {
		t.Fatalf("Same(a,b) = (%t,%v), want true", same, err)
	}
}

func TestNePropagatesPinnedValueAndDetectsEqualPinnedClasses(t *testing.T) {
	d := NewDeriver()
	addForTest(t, d, NewNe("x", "y"))
	addForTest(t, d, NewCmp("x", OpEqual, 3))
	assertPinned(t, d, "x", 3, true)
	assertRange(t, d, "y", MinInt64, MaxInt64)
	assertExcluded(t, d, "y", []int64{3})

	addForTest(t, d, NewCmp("p", OpEqual, 1))
	addForTest(t, d, NewCmp("q", OpEqual, 1))
	err := d.Add(NewNe("p", "q"))
	t.Logf("Add Ne(p,q) after both pinned to 1 => %v (want %v)", err, ErrContradiction)
	if !errors.Is(err, ErrContradiction) {
		t.Fatalf("Ne equal pinned classes: %v, want contradiction", err)
	}
}

func TestNeSameColumnContradicts(t *testing.T) {
	d := NewDeriver()
	err := d.Add(NewNe("a", "a"))
	t.Logf("Add Ne(a,a) => %v (want %v); rejected input must not register a", err, ErrContradiction)
	if !errors.Is(err, ErrContradiction) {
		t.Fatalf("Ne(a,a): %v, want contradiction", err)
	}
	_, _, err = d.Range("a")
	if !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("Range(a) after rejected Ne(a,a): %v, want unknown column", err)
	}
}

func TestRejectedInputDoesNotRegisterColumn(t *testing.T) {
	d := NewDeriver()
	err := d.Add(NewCmp("", OpLess, MinInt64))
	t.Logf("invalid-before-contradiction input => %v (want %v)", err, ErrInvalidPredicate)
	if !errors.Is(err, ErrInvalidPredicate) {
		t.Fatalf("invalid predicate: %v, want invalid predicate", err)
	}

	err = d.Add(NewCmp("z", OpLess, MinInt64))
	t.Logf("contradictory new column z < MinInt64 => %v (want %v)", err, ErrContradiction)
	if !errors.Is(err, ErrContradiction) {
		t.Fatalf("z < MinInt64: %v, want contradiction", err)
	}
	_, _, err = d.Range("z")
	if !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("rejected new column z: %v, want unknown column", err)
	}
}

func TestImpliesConditionsAndErrorPriority(t *testing.T) {
	d := NewDeriver()
	addForTest(t, d, NewCmp("x", OpGreaterEqual, 3))
	addForTest(t, d, NewCmp("x", OpLessEqual, 5))
	addForTest(t, d, NewCmp("y", OpEqual, 5))
	addForTest(t, d, NewCmp("z", OpEqual, 5))
	addForTest(t, d, NewEq("x", "y"))
	addForTest(t, d, NewCmp("w", OpGreaterEqual, 3))
	addForTest(t, d, NewCmp("w", OpLessEqual, 6))

	cases := []struct {
		name string
		pred Predicate
		want bool
	}{
		{"equal pinned value", NewCmp("x", OpEqual, 5), true},
		{"equal other value", NewCmp("x", OpEqual, 4), false},
		{"not equal below range", NewCmp("x", OpNotEqual, 2), true},
		{"not equal inside range", NewCmp("w", OpNotEqual, 4), false},
		{"less", NewCmp("x", OpLess, 6), true},
		{"less equal boundary", NewCmp("x", OpLess, 5), false},
		{"less equal", NewCmp("x", OpLessEqual, 5), true},
		{"greater", NewCmp("x", OpGreater, 4), true},
		{"greater equal", NewCmp("x", OpGreaterEqual, 5), true},
		{"eq same class", NewEq("x", "y"), true},
		{"eq separately pinned same value", NewEq("y", "z"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.Implies(tc.pred)
			t.Logf("Implies(%v) => got=%t err=%v want=%t", tc.pred, got, err, tc.want)
			if err != nil || got != tc.want {
				t.Fatalf("Implies(%v) = (%t,%v), want %t", tc.pred, got, err, tc.want)
			}
		})
	}

	if _, err := d.Implies(NewNe("x", "")); !errors.Is(err, ErrInvalidPredicate) {
		t.Fatalf("Implies(Ne with empty column): %v, want invalid predicate", err)
	}
	if _, err := d.Implies(NewNe("x", "missing")); !errors.Is(err, ErrInvalidPredicate) {
		t.Fatalf("Implies(Ne): %v, want invalid predicate", err)
	}
	if _, err := d.Implies(NewEq("", "missing")); !errors.Is(err, ErrInvalidPredicate) {
		t.Fatalf("Implies(Eq with empty first column): %v, want invalid predicate", err)
	}
	if _, err := d.Implies(NewEq("missing", "x")); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("Implies(Eq unknown column): %v, want unknown column", err)
	}
	if _, err := d.Implies(NewCmp("missing", OpEqual, 5)); !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("Implies(Cmp unknown column): %v, want unknown column", err)
	}
}

func TestConcurrentAddsAndQueriesAreSafe(t *testing.T) {
	d := NewDeriver()
	preds := []Predicate{
		NewCmp("x", OpEqual, 0),
		NewCmp("x", OpGreaterEqual, 0),
		NewCmp("x", OpLessEqual, 0),
		NewCmp("x", OpNotEqual, 1),
	}
	rejected := []Predicate{
		NewCmp("x", OpEqual, 1),
		NewCmp("x", OpLess, 0),
		NewCmp("x", OpGreater, 0),
		NewCmp("x", OpNotEqual, 0),
		NewCmp("", OpEqual, 0),
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 100; iteration++ {
				pred := preds[(worker+iteration)%len(preds)]
				if err := d.Add(pred); err != nil {
					t.Errorf("concurrent Add(%v): %v", pred, err)
					return
				}
				if err := d.Add(rejected[iteration%len(rejected)]); err == nil {
					t.Error("expected concurrent rejected Add to fail")
					return
				}
				lo, hi, err := d.Range("x")
				if err != nil {
					t.Errorf("concurrent Range: %v", err)
					return
				}
				if lo < MinInt64 || hi > MaxInt64 || lo > hi {
					t.Errorf("concurrent Range returned invalid interval [%d,%d]", lo, hi)
				}
			}
		}(worker)
	}
	wg.Wait()

	assertRange(t, d, "x", 0, 0)
	assertPinned(t, d, "x", 0, true)
	_, _, err := d.Range("missing-from-rejected-input")
	if !errors.Is(err, ErrUnknownColumn) {
		t.Fatalf("rejected concurrent input registered column: %v", err)
	}
}
