package ontology

import (
	"math"
	"testing"
)

// singleGroupResult feeds rows into a fresh single-key aggregator and
// returns the sole GroupResult. All tests in this file characterize the
// CURRENT behavior of the aggregator, including cases where that
// behavior is arguably wrong; they must pass against the unmodified
// implementation.
func singleGroupResult(t *testing.T, rows ...map[string]any) GroupResult {
	t.Helper()
	agg := NewAggregator([]string{"g"}, "v")
	for _, row := range rows {
		agg.Add(row)
	}
	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d groups, want 1", len(results))
	}
	return results[0]
}

// A group whose rows are ALL non-summable is reported as a pure int64
// group with sum 0, because result() treats "never saw a float64" as
// "only int64 values were added", even when no summable value at all was
// seen. This pins the exact fields for each non-summable row shape.
func TestCharacterizeAllNonSummableGroup(t *testing.T) {
	cases := []struct {
		name string
		row  map[string]any
	}{
		{"missing_column", map[string]any{"g": "a"}},
		{"nil_value", map[string]any{"g": "a", "v": nil}},
		{"string", map[string]any{"g": "a", "v": "bad"}},
		{"bool", map[string]any{"g": "a", "v": true}},
		{"nan", map[string]any{"g": "a", "v": math.NaN()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := singleGroupResult(t, tc.row)
			want := GroupResult{
				Count:    1,
				Skipped:  1,
				IsInt:    true,
				IntSum:   0,
				FloatSum: 0,
			}
			if r.Count != want.Count || r.Skipped != want.Skipped ||
				r.IsInt != want.IsInt || r.IntSum != want.IntSum ||
				r.FloatSum != want.FloatSum {
				t.Fatalf("all-skipped (%s) = %+v, want %+v", tc.name, r, want)
			}
			if r.Sum() != 0 {
				t.Fatalf("Sum() = %v, want 0", r.Sum())
			}
		})
	}

	// Several different non-summable rows in one group collapse to the
	// same shape: still IsInt=true, IntSum=0.
	mixed := singleGroupResult(t,
		map[string]any{"g": "a"},
		map[string]any{"g": "a", "v": nil},
		map[string]any{"g": "a", "v": "x"},
		map[string]any{"g": "a", "v": false},
		map[string]any{"g": "a", "v": math.NaN()},
	)
	if mixed.Count != 5 || mixed.Skipped != 5 ||
		!mixed.IsInt || mixed.IntSum != 0 || mixed.FloatSum != 0 {
		t.Fatalf("mixed all-skipped group = %+v", mixed)
	}
}

// A genuine int64 zero sum (5 + -5) produces the SAME sum-related
// fields as a group with no summable rows at all: IsInt=true,
// IntSum=0, FloatSum=0. Only Count/Skipped differ.
func TestCharacterizeZeroSumVsNoSummableRows(t *testing.T) {
	zero := singleGroupResult(t,
		map[string]any{"g": "a", "v": int64(5)},
		map[string]any{"g": "a", "v": int64(-5)},
	)
	empty := singleGroupResult(t,
		map[string]any{"g": "a"},
		map[string]any{"g": "a", "v": "x"},
	)

	if zero.Count != 2 || zero.Skipped != 0 {
		t.Fatalf("zero group count/skipped = %d/%d, want 2/0",
			zero.Count, zero.Skipped)
	}
	if empty.Count != 2 || empty.Skipped != 2 {
		t.Fatalf("empty group count/skipped = %d/%d, want 2/2",
			empty.Count, empty.Skipped)
	}

	// Sum-bearing fields are pairwise identical, so downstream code that
	// looks only at IsInt/IntSum/FloatSum (or Sum()) cannot tell the two
	// groups apart.
	if zero.IsInt != empty.IsInt ||
		zero.IntSum != empty.IntSum ||
		zero.FloatSum != empty.FloatSum ||
		zero.Sum() != empty.Sum() {
		t.Fatalf("sum fields differ: zero=%+v empty=%+v", zero, empty)
	}
	if zero.IsInt != true || zero.IntSum != 0 ||
		zero.FloatSum != 0 || zero.Sum() != 0 {
		t.Fatalf("zero group = %+v, want IsInt true IntSum 0", zero)
	}
}

// addValue's type switch only matches int64 and float64. Every other Go
// integer kind falls into default and is silently skipped (counted in
// Skipped, never added). Note that an untyped integer literal placed in
// an any becomes int, so Add({"v": 5}) is skipped, not summed.
func TestCharacterizeNonInt64IntegerKinds(t *testing.T) {
	cases := []struct {
		name      string
		value     any
		wantSum   int64
		wantSkip  int64
		wantIsInt bool
	}{
		{"int", int(5), 0, 1, true},
		{"uint", uint(5), 0, 1, true},
		{"int32", int32(5), 0, 1, true},
		{"int64", int64(5), 5, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Constructed via a map so the value really goes through the
			// any -> type-switch path used by Add.
			r := singleGroupResult(t, map[string]any{"g": "a", "v": tc.value})
			if r.Count != 1 {
				t.Fatalf("Count = %d, want 1", r.Count)
			}
			if r.Skipped != tc.wantSkip {
				t.Fatalf("%s: Skipped = %d, want %d",
					tc.name, r.Skipped, tc.wantSkip)
			}
			if r.IsInt != tc.wantIsInt || r.IntSum != tc.wantSum {
				t.Fatalf("%s: IsInt=%v IntSum=%d, want %v %d",
					tc.name, r.IsInt, r.IntSum, tc.wantIsInt, tc.wantSum)
			}
		})
	}

	// Untyped literal 5 in an any is an int and is therefore dropped.
	literal := singleGroupResult(t, map[string]any{"g": "a", "v": 5})
	if literal.Skipped != 1 || literal.IntSum != 0 || !literal.IsInt {
		t.Fatalf("untyped literal 5 = %+v, want skipped int zero", literal)
	}
}

// Through Snapshot(), the zero-sum group and the all-skipped group can
// only be told apart via Count/Skipped: every sum-related public field,
// including the derived Sum(), is identical. This pins that the
// distinction exists at all, and exactly where it does and does not.
func TestCharacterizeSnapshotZeroVsSkipped(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	// Sorted key order: "empty" before "zero".
	agg.Add(map[string]any{"g": "empty", "v": "x"})
	agg.Add(map[string]any{"g": "zero", "v": int64(0)})

	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d groups, want 2", len(results))
	}
	empty, zero := results[0], results[1]

	// With equal row counts the ONLY differing public fields are Skipped.
	if empty.Count != zero.Count {
		t.Fatalf("Count: empty=%d zero=%d, want equal",
			empty.Count, zero.Count)
	}
	if empty.Skipped == zero.Skipped {
		t.Fatal("Skipped must differ between the groups")
	}
	if empty.IsInt != zero.IsInt ||
		empty.IntSum != zero.IntSum ||
		empty.FloatSum != zero.FloatSum ||
		empty.Sum() != zero.Sum() {
		t.Fatalf("sum fields must be identical: empty=%+v zero=%+v",
			empty, zero)
	}
	if !empty.IsInt || empty.IntSum != 0 || empty.Sum() != 0 {
		t.Fatalf("all-skipped group reported as %+v", empty)
	}
	if !zero.IsInt || zero.IntSum != 0 || zero.Sum() != 0 {
		t.Fatalf("zero-sum group reported as %+v", zero)
	}
}
