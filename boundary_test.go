package ontology

import (
	"math"
	"testing"
)

// Characterization tests: these pin the CURRENT behavior of the
// aggregator at the "summable value" boundary. They are not a
// specification of desired behavior; see FINDINGS.md for the gaps
// they expose.

// snapshotOne feeds rows into a fresh single-key aggregator and
// returns the single resulting group.
func snapshotOne(t *testing.T, rows []map[string]any) GroupResult {
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

// A group whose rows are ALL non-summable collapses to the exact same
// sum fields as a group that genuinely summed to int64(0):
// IsInt=true, IntSum=0, FloatSum=0. Only Count/Skipped differ.
func TestAllUnsummableGroupCharacterization(t *testing.T) {
	cases := []struct {
		name string
		rows []map[string]any
	}{
		{"missing column", []map[string]any{{"g": "a"}}},
		{"string value", []map[string]any{{"g": "a", "v": "bad"}}},
		{"bool value", []map[string]any{{"g": "a", "v": true}}},
		{"NaN value", []map[string]any{{"g": "a", "v": math.NaN()}}},
		{"nil value", []map[string]any{{"g": "a", "v": nil}}},
		{"mixed unsummable", []map[string]any{
			{"g": "a"},
			{"g": "a", "v": "bad"},
			{"g": "a", "v": false},
			{"g": "a", "v": math.NaN()},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := snapshotOne(t, tc.rows)
			wantCount := int64(len(tc.rows))
			if r.Count != wantCount {
				t.Errorf("Count = %d, want %d", r.Count, wantCount)
			}
			if r.Skipped != wantCount {
				t.Errorf("Skipped = %d, want %d (every row skipped)", r.Skipped, wantCount)
			}
			// The empty sum is folded into the "pure int64" branch.
			if !r.IsInt {
				t.Errorf("IsInt = false, want true (no float64 ever seen)")
			}
			if r.IntSum != 0 {
				t.Errorf("IntSum = %d, want 0", r.IntSum)
			}
			if r.FloatSum != 0 {
				t.Errorf("FloatSum = %v, want 0", r.FloatSum)
			}
			if got := r.Sum(); got != 0 {
				t.Errorf("Sum() = %v, want 0", got)
			}
		})
	}
}

// Contrast: a group that really accumulates to int64(0) produces the
// identical IsInt/IntSum/FloatSum triple as an all-skipped group.
func TestZeroSumGroupCharacterization(t *testing.T) {
	zeroSum := snapshotOne(t, []map[string]any{
		{"g": "a", "v": int64(5)},
		{"g": "a", "v": int64(-5)},
	})
	if !zeroSum.IsInt || zeroSum.IntSum != 0 || zeroSum.FloatSum != 0 {
		t.Fatalf("zero-sum group = %+v, want IsInt=true IntSum=0 FloatSum=0", zeroSum)
	}
	if zeroSum.Count != 2 || zeroSum.Skipped != 0 {
		t.Fatalf("zero-sum group Count/Skipped = %d/%d, want 2/0",
			zeroSum.Count, zeroSum.Skipped)
	}

	noData := snapshotOne(t, []map[string]any{
		{"g": "a", "v": "bad"},
		{"g": "a"},
	})
	// The sum-related public fields are field-by-field identical.
	if zeroSum.IsInt != noData.IsInt ||
		zeroSum.IntSum != noData.IntSum ||
		math.Float64bits(zeroSum.FloatSum) != math.Float64bits(noData.FloatSum) {
		t.Fatalf("sum fields differ: zero-sum %+v vs no-data %+v", zeroSum, noData)
	}
	// Count and Skipped are the ONLY public fields that differ, and
	// only because this particular no-data group skipped every row.
	if zeroSum.Count == noData.Count && zeroSum.Skipped == noData.Skipped {
		t.Fatal("expected Count/Skipped to differ in this setup")
	}
}

// addValue's type switch recognizes only int64 and float64. Every
// other Go integer type is silently skipped: counted in Skipped,
// excluded from the sum, no error, no conversion.
func TestIntegerTypeDispatchCharacterization(t *testing.T) {
	cases := []struct {
		name    string
		value   any
		summed  bool // current behavior: is the value included in the sum?
		wantSum float64
	}{
		{"int", int(5), false, 0},
		{"int8", int8(5), false, 0},
		{"int16", int16(5), false, 0},
		{"int32", int32(5), false, 0},
		{"int64", int64(5), true, 5},
		{"uint", uint(5), false, 0},
		{"uint8", uint8(5), false, 0},
		{"uint16", uint16(5), false, 0},
		{"uint32", uint32(5), false, 0},
		{"uint64", uint64(5), false, 0},
		{"float32", float32(5), false, 0},
		{"float64", float64(5), true, 5},
		{"untyped literal 5 is int", 5, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := snapshotOne(t, []map[string]any{{"g": "a", "v": tc.value}})
			if r.Count != 1 {
				t.Fatalf("Count = %d, want 1", r.Count)
			}
			if tc.summed {
				if r.Skipped != 0 {
					t.Errorf("Skipped = %d, want 0 (type %T is summed)", r.Skipped, tc.value)
				}
				if r.Sum() != tc.wantSum {
					t.Errorf("Sum() = %v, want %v", r.Sum(), tc.wantSum)
				}
			} else {
				if r.Skipped != 1 {
					t.Errorf("Skipped = %d, want 1 (type %T silently skipped)", r.Skipped, tc.value)
				}
				// Silently dropped: the group looks exactly like an
				// empty-int group with sum zero.
				if !r.IsInt || r.IntSum != 0 {
					t.Errorf("sum = (IsInt=%v, IntSum=%d), want (true, 0)", r.IsInt, r.IntSum)
				}
			}
		})
	}
}

// End-to-end via Snapshot: given only the public GroupResult fields,
// a zero-sum int64 group and an all-skipped group cannot be told
// apart by their sum fields. The only observable signal is the
// undocumented invariant Skipped == Count for a no-data group.
func TestSnapshotCannotDistinguishZeroSumFromNoData(t *testing.T) {
	agg := NewAggregator([]string{"g"}, "v")
	agg.Add(map[string]any{"g": "zero", "v": int64(5)})
	agg.Add(map[string]any{"g": "zero", "v": int64(-5)})
	agg.Add(map[string]any{"g": "empty", "v": "bad"})
	agg.Add(map[string]any{"g": "empty"}) // missing column

	results, err := agg.Snapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d groups, want 2", len(results))
	}
	byName := map[string]GroupResult{}
	for _, r := range results {
		byName[r.Key[0].Value.(string)] = r
	}
	zero, empty := byName["zero"], byName["empty"]

	if zero.IsInt != empty.IsInt || zero.IntSum != empty.IntSum ||
		math.Float64bits(zero.FloatSum) != math.Float64bits(empty.FloatSum) {
		t.Fatalf("sum fields distinguishable: zero=%+v empty=%+v", zero, empty)
	}
	if zero.Sum() != empty.Sum() {
		t.Fatalf("Sum() distinguishes: zero=%v empty=%v", zero.Sum(), empty.Sum())
	}
	// The sole distinguishing signal today: every row was skipped.
	if empty.Skipped != empty.Count {
		t.Fatalf("empty group Skipped=%d Count=%d, want equal", empty.Skipped, empty.Count)
	}
	if zero.Skipped == zero.Count {
		t.Fatalf("zero-sum group Skipped=%d Count=%d, want different", zero.Skipped, zero.Count)
	}
}
