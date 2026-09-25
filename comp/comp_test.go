package comp

import (
	"errors"
	"reflect"
	"testing"

	"ontology/seg"
)

func rec(k string, v int64, op, val string) seg.Rec {
	return seg.Rec{Key: k, Version: v, Op: op, Val: val}
}

func eightRecs() []seg.Rec {
	return []seg.Rec{
		rec("a", 1, seg.OpPut, "x"), rec("b", 2, seg.OpPut, "y"),
		rec("a", 3, seg.OpPut, "x2"), rec("c", 4, seg.OpPut, "z"),
		rec("b", 5, seg.OpDel, ""), rec("a", 6, seg.OpDel, ""),
		rec("c", 7, seg.OpPut, "z2"), rec("b", 8, seg.OpPut, "y2"),
	}
}

// Invariant 2: one record per key, key-sorted, no tombstone survives;
// section 3 (甲) a stays absent (ignoring the v6 tombstone would wrongly
// resurrect a=x2), (乙) watermark is global max 8, not v7 of the last
// key-sorted row, (丙) segment order [seg2, seg1] must not let seg1
// overwrite: b stays y2 (not deleted), c stays z2 (not z).
func TestCompactOutputInvariants(t *testing.T) {
	rs := eightRecs()
	s1, s2 := &Segment{Records: rs[:5]}, &Segment{Records: rs[5:]}
	want := []seg.Rec{rec("b", 8, seg.OpPut, "y2"), rec("c", 7, seg.OpPut, "z2")}
	cases := map[string][]*Segment{
		"seg2 then seg1": {s2, s1},
		"seg1 then seg2": {s1, s2},
	}
	for name, in := range cases {
		out, err := NewMerger().Compact(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(out.Records, want) {
			t.Errorf("%s: records=%v want %v", name, out.Records, want)
		}
		if out.Watermark != 8 {
			t.Errorf("%s: watermark=%d want 8", name, out.Watermark)
		}
	}
}

// After m historical puts compact away, one more put must locate the
// winner with a constant number of comparisons independent of m: the
// compacted segment holds one pointer for the key, so the extra merge
// compares exactly once instead of re-scanning m history rows.
func TestPointerSeekConstantComparisons(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		mg := NewMerger()
		hist := make([]seg.Rec, m)
		for i := range hist {
			hist[i] = rec("k", int64(i+1), seg.OpPut, "v")
		}
		base, err := mg.Compact([]*Segment{{Records: hist}})
		if err != nil {
			t.Fatalf("m=%d build: %v", m, err)
		}
		if mg.cmpCount != m-1 { // accumulation reads m rows; counter is live
			t.Fatalf("m=%d build comparisons=%d want %d", m, mg.cmpCount, m-1)
		}
		more := &Segment{Records: []seg.Rec{rec("k", int64(m+1), seg.OpPut, "w")}}
		if _, err := mg.Compact([]*Segment{base, more}); err != nil {
			t.Fatalf("m=%d append: %v", m, err)
		}
		if mg.cmpCount != 1 {
			t.Errorf("m=%d winner comparisons=%d, want 1 independent of m", m, mg.cmpCount)
		}
	}
}

func TestCompactRejectsBadRecords(t *testing.T) {
	bad := []seg.Rec{
		rec("", 1, seg.OpPut, "q"),
		rec("k", 1, "patch", "q"),
		rec("k", 1, seg.OpPut, ""),
		rec("k", 1, seg.OpDel, "q"),
	}
	want := []error{seg.ErrEmptyKey, seg.ErrBadOp, seg.ErrBadVal, seg.ErrBadVal}
	for i, r := range bad {
		mg := NewMerger()
		out, err := mg.Compact([]*Segment{{Records: []seg.Rec{r}}})
		if !errors.Is(err, want[i]) || out != nil {
			t.Errorf("case %d: err=%v out=%v, want %v", i, err, out, want[i])
		}
		if mg.cmpCount != 0 {
			t.Errorf("case %d: comparisons counted on failed merge: %d", i, mg.cmpCount)
		}
	}
}
