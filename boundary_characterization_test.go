package ontology

import (
	"math"
	"testing"
)

// Characterization tests for the two untested boundaries of the
// summable-value space:
//
//  1. A group in which every row is non-summable never sees a float64,
//     so result() takes the "pure int64" branch and folds the empty
//     sum into IsInt=true, IntSum=0 — identical field-by-field to a
//     group whose int64 values genuinely add up to zero, apart from
//     Count/Skipped.
//  2. addValue's type switch matches only int64 and float64; every
//     other Go integer kind (int, uint, int32, ...) falls through to
//     default and is silently counted in Skipped.
//
// These tests pin the CURRENT behavior, not a desired one.

// snapGroup feeds rows into a fresh single-group aggregator and
// returns the one GroupResult, failing if Snapshot errors.
func snapGroup(t *testing.T, rows ...map[string]any) GroupResult {
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

// All-unsummable groups collapse onto the same Sum-bearing fields
// (IsInt/IntSum/FloatSum) regardless of why every row was skipped;
// only Count/Skipped reveal what happened.
func TestCharacterizeAllUnsummableGroup(t *testing.T) {
	cases := []struct {
		name string
		row  map[string]any
	}{
		{"missing_column", map[string]any{"g": "a"}},
		{"string", map[string]any{"g": "a", "v": "x"}},
		{"bool", map[string]any{"g": "a", "v": true}},
		{"nan", map[string]any{"g": "a", "v": math.NaN()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := snapGroup(t, tc.row)
			if r.Count != 1 || r.Skipped != 1 {
				t.Fatalf("Count=%d Skipped=%d, want Count=1 Skipped=1",
					r.Count, r.Skipped)
			}
			// CURRENT behavior: never saw a float64, so the empty
			// rational reads out as a pure-int64 zero.
			if !r.IsInt {
				t.Fatalf("IsInt=false, want true (empty sum misread as int64)")
			}
			if r.IntSum != 0 {
				t.Fatalf("IntSum=%d, want 0", r.IntSum)
			}
			if r.FloatSum != 0 {
				t.Fatalf("FloatSum=%v, want 0", r.FloatSum)
			}
		})
	}
}

// Several consecutive unsummable rows in one group produce the same
// folded shape, with all of them counted in Skipped.
func TestCharacterizeAllUnsummableMultipleRows(t *testing.T) {
	r := snapGroup(t,
		map[string]any{"g": "a"},
		map[string]any{"g": "a", "v": "x"},
		map[string]any{"g": "a", "v": false},
		map[string]any{"g": "a", "v": math.NaN()},
	)
	if r.Count != 4 || r.Skipped != 4 {
		t.Fatalf("Count=%d Skipped=%d, want 4/4", r.Count, r.Skipped)
	}
	if !r.IsInt || r.IntSum != 0 || r.FloatSum != 0 {
		t.Fatalf("sum fields = IsInt=%v IntSum=%d FloatSum=%v, "+
			"want IsInt=true IntSum=0 FloatSum=0",
			r.IsInt, r.IntSum, r.FloatSum)
	}
}

// A genuine int64 zero sum reports exactly the same IsInt/IntSum/
// FloatSum as an all-unsummable group. The ONLY public-field
// distinction is Count vs Skipped: here Skipped is 0.
func TestCharacterizeRealZeroVsAllSkipped(t *testing.T) {
	zero := snapGroup(t,
		map[string]any{"g": "a", "v": int64(5)},
		map[string]any{"g": "a", "v": int64(-5)},
	)
	empty := snapGroup(t,
		map[string]any{"g": "a"},
		map[string]any{"g": "a", "v": math.NaN()},
	)

	if !zero.IsInt || zero.IntSum != 0 || zero.FloatSum != 0 {
		t.Fatalf("zero group = %+v, want IsInt=true IntSum=0 FloatSum=0", zero)
	}
	if !empty.IsInt || empty.IntSum != 0 || empty.FloatSum != 0 {
		t.Fatalf("empty group = %+v, want IsInt=true IntSum=0 FloatSum=0", empty)
	}
	// The sum-bearing fields are indistinguishable.
	if zero.IsInt != empty.IsInt ||
		zero.IntSum != empty.IntSum ||
		math.Float64bits(zero.FloatSum) != math.Float64bits(empty.FloatSum) ||
		zero.Sum() != empty.Sum() {
		t.Fatalf("zero-sum %+v and all-skipped %+v differ in Sum fields",
			zero, empty)
	}
	// Count happens to coincide here too; only Skipped separates them.
	if zero.Count != empty.Count {
		t.Fatalf("Count differs: zero=%d empty=%d", zero.Count, empty.Count)
	}
	if zero.Skipped != 0 || empty.Skipped != 2 {
		t.Fatalf("Skipped: zero=%d want 0, empty=%d want 2",
			zero.Skipped, empty.Skipped)
	}
}

// Every non-int64 Go integer kind is silently skipped: not summed,
// but counted in Skipped exactly like a string. int64 is the sole
// integer kind that participates in the sum.
func TestCharacterizeNonInt64KindsSkipped(t *testing.T) {
	cases := []struct {
		name     string
		value    any
		wantSum  int64
		wantSkip int64
		summable bool
	}{
		{"int", int(5), 0, 1, false},
		{"uint", uint(5), 0, 1, false},
		{"int32", int32(5), 0, 1, false},
		{"uint64", uint64(5), 0, 1, false},
		{"int64", int64(5), 5, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := snapGroup(t, map[string]any{"g": "a", "v": tc.value})
			if r.Count != 1 {
				t.Fatalf("Count=%d, want 1", r.Count)
			}
			if r.Skipped != tc.wantSkip {
				t.Fatalf("Skipped=%d, want %d for kind %s",
					r.Skipped, tc.wantSkip, tc.name)
			}
			if !r.IsInt || r.IntSum != tc.wantSum {
				t.Fatalf("IsInt=%v IntSum=%d, want true / %d for kind %s",
					r.IsInt, r.IntSum, tc.wantSum, tc.name)
			}
		})
	}
}

// An integer literal stored in a map[string]any takes on dynamic type
// int (not int64), so the most natural Add call site is dropped from
// the sum without any error.
func TestCharacterizeBareLiteralIsIntAndSkipped(t *testing.T) {
	r := snapGroup(t, map[string]any{"g": "a", "v": 5})
	if r.Count != 1 || r.Skipped != 1 {
		t.Fatalf("Count=%d Skipped=%d, want 1/1", r.Count, r.Skipped)
	}
	if !r.IsInt || r.IntSum != 0 {
		t.Fatalf("IsInt=%v IntSum=%d, want true/0: bare literal 5 was dropped",
			r.IsInt, r.IntSum)
	}
}

// At the Snapshot/GroupResult level, a zero-sum group and an
// all-skipped group cannot be told apart through IsInt/IntSum/
// FloatSum/Sum(); a consumer must infer "no summable data" indirectly
// from Skipped == Count.
func TestCharacterizeSnapshotZeroIndistinguishableFromEmpty(t *testing.T) {
	makeResults := func() []GroupResult {
		agg := NewAggregator([]string{"g"}, "v")
		agg.Add(map[string]any{"g": "zero", "v": int64(5)})
		agg.Add(map[string]any{"g": "zero", "v": int64(-5)})
		agg.Add(map[string]any{"g": "empty"})
		agg.Add(map[string]any{"g": "empty", "v": "bad"})
		res, err := agg.Snapshot()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return res
	}
	results := makeResults()
	if len(results) != 2 {
		t.Fatalf("got %d groups, want 2", len(results))
	}
	empty, zero := results[0], results[1] // "empty" < "zero"
	if got := KeyString(empty.Key); got != "(empty)" {
		t.Fatalf("results order wrong: first key = %s", got)
	}
	// Identical public sum surface.
	if empty.IsInt != zero.IsInt ||
		empty.IntSum != zero.IntSum ||
		math.Float64bits(empty.FloatSum) != math.Float64bits(zero.FloatSum) {
		t.Fatalf("Sum fields differ:\n empty=%+v\n zero =%+v", empty, zero)
	}
	// CURRENT workaround available to downstream code: only the
	// Skipped==Count predicate identifies the no-data group.
	if empty.Count != 2 || empty.Skipped != 2 {
		t.Fatalf("empty Count=%d Skipped=%d, want 2/2",
			empty.Count, empty.Skipped)
	}
	if zero.Count != 2 || zero.Skipped != 0 {
		t.Fatalf("zero Count=%d Skipped=%d, want 2/0",
			zero.Count, zero.Skipped)
	}
	if empty.Skipped == empty.Count && zero.Skipped == zero.Count {
		t.Fatal("both groups look all-skipped; Skipped==Count predicate broken")
	}
}
