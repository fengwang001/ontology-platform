package keyenc

import (
	"bytes"
	"testing"
)

func TestEncodingsRoundTripAndOrder(t *testing.T) {
	keys := []int64{MinKey, -3, -1, 0, 1, 2, MaxKey}
	for _, k := range keys {
		if Decode1(Encode1(k)) != k {
			t.Fatalf("E1 round trip failed for %d", k)
		}
		if Decode2(Encode2(k)) != k {
			t.Fatalf("E2 round trip failed for %d", k)
		}
	}
	// E2 is order preserving.
	for i := 0; i+1 < len(keys); i++ {
		if bytes.Compare(Encode2(keys[i]), Encode2(keys[i+1])) >= 0 {
			t.Fatalf("E2 order violated: %d !< %d", keys[i], keys[i+1])
		}
	}
	// E1 is not order preserving across the sign boundary.
	if bytes.Compare(Encode1(-1), Encode1(0)) <= 0 {
		t.Fatal("E1 negatives must sort after non-negatives")
	}
}

// collectRange scans the physical ranges over a present-key set and returns
// the decoded logical keys, in range order.
func collectRanges(t *testing.T, ranges []Range, present map[int64]bool, e2 bool) []int64 {
	t.Helper()
	got := make([]int64, 0)
	for _, r := range ranges {
		for k := range present {
			var enc []byte
			if !e2 {
				enc = Encode1(k)
			} else {
				enc = Encode2(k)
			}
			if bytes.Compare(enc, r.Low) < 0 {
				continue
			}
			if r.High != nil && bytes.Compare(enc, r.High) >= 0 {
				continue
			}
			if e2 {
				got = append(got, Decode2(enc))
			} else {
				got = append(got, Decode1(enc))
			}
		}
	}
	return got
}

func TestE1RangesNegativeInfinityBound(t *testing.T) {
	// Example two: [-3, 0) maps to [E1(-3), +inf), covering -3 and -1.
	ranges := E1Ranges(-3, 0)
	if len(ranges) != 1 {
		t.Fatalf("want 1 negative range, got %d", len(ranges))
	}
	if ranges[0].High != nil {
		t.Fatal("negative segment reaches physical end: high must be nil (+inf)")
	}
	present := map[int64]bool{-3: true, -1: true, 0: true, 2: true}
	got := collectRanges(t, ranges, present, false)
	want := []int64{-3, -1}
	if len(got) != len(want) {
		t.Fatalf("negative infinity segment: got %v, want %v", got, want)
	}
}

func TestE1RangesSplitAndBoundaries(t *testing.T) {
	present := map[int64]bool{-5: true, -2: true, -1: true, 0: true, 1: true, 5: true}
	cases := []struct {
		lo, hi int64
		want   []int64
	}{
		{-5, 6, []int64{-5, -2, -1, 0, 1, 5}},
		{-1, 6, []int64{-1, 0, 1, 5}}, // lo on boundary w=-1
		{0, 6, []int64{0, 1, 5}},      // lo exactly 0
		{-5, 0, []int64{-5, -2, -1}},  // hi exactly 0 -> infinite bound
		{-2, 0, []int64{-2, -1}},
		{1, 1, nil}, // empty interval
	}
	for _, c := range cases {
		ranges := E1Ranges(c.lo, c.hi)
		got := collectRanges(t, ranges, present, false)
		if len(ranges) == 2 && bytes.Compare(ranges[0].Low, ranges[1].Low) < 0 {
			t.Fatalf("[%d,%d): negative physical range must start after non-negative range bytes", c.lo, c.hi)
		}
		if len(got) != len(c.want) {
			t.Fatalf("[%d,%d): got %v, want %v", c.lo, c.hi, got, c.want)
		}
	}
}

func TestE2RangesSingleAndEmpty(t *testing.T) {
	if got := E2Ranges(3, 3); got != nil {
		t.Fatalf("empty interval yields no ranges, got %v", got)
	}
	ranges := E2Ranges(-5, 4)
	if len(ranges) != 1 {
		t.Fatalf("E2 interval maps to one range, got %d", len(ranges))
	}
	present := map[int64]bool{-5: true, -2: true, 0: true, 3: true, 4: true, 9: true}
	got := collectRanges(t, ranges, present, true)
	want := []int64{-5, -2, 0, 3}
	if len(got) != len(want) {
		t.Fatalf("E2 range got %v, want %v", got, want)
	}
}
