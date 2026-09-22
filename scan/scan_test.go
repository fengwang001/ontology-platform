package scan

import (
	"math/rand"
	"testing"

	"ontology/segment"
	"ontology/zone"
)

func buildSeg(t *testing.T, groups []struct {
	vals  []int64
	nulls []bool
}) *segment.Segment {
	t.Helper()
	b := segment.NewBuilder(segment.Config{MaxDictCard: 8})
	for _, g := range groups {
		if err := b.AddRowGroup(g.vals, g.nulls); err != nil {
			t.Fatal(err)
		}
	}
	seg, err := segment.Open(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return seg
}

func g(vals ...int64) struct {
	vals  []int64
	nulls []bool
} {
	return struct {
		vals  []int64
		nulls []bool
	}{vals, make([]bool, len(vals))}
}

// TestPruningCounters: 1000 row groups, a predicate that only one
// group can satisfy; exactly one group may be decoded.
func TestPruningCounters(t *testing.T) {
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	for i := 0; i < 1000; i++ {
		row := g()
		for j := 0; j < 100; j++ {
			row.vals = append(row.vals, int64(i))
			row.nulls = append(row.nulls, false)
		}
		groups = append(groups, row)
	}
	seg := buildSeg(t, groups)
	sc := NewScanner(seg, []zone.Pred{zone.Equal(537)})
	rows := sc.All()
	if len(rows) != 100 {
		t.Fatalf("got %d rows, want 100", len(rows))
	}
	if seg.DecodedGroups() != 1 {
		t.Fatalf("decoded %d groups, want 1 of 1000", seg.DecodedGroups())
	}
	if seg.DecodedValues() > 100 {
		t.Fatalf("decoded %d values, want <= 100", seg.DecodedValues())
	}
}

// TestExhaustiveEquivalence compares pushdown results against
// decode-everything-then-filter on random data, including boundary
// predicates (equal to min/max, above max, below min, tight ranges).
func TestExhaustiveEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	var allVals []int64
	var allNulls []bool
	for gi := 0; gi < 40; gi++ {
		gr := g()
		n := 1 + rng.Intn(50)
		for j := 0; j < n; j++ {
			v := int64(rng.Intn(61) - 30)
			isNull := rng.Intn(6) == 0
			gr.vals = append(gr.vals, v)
			gr.nulls = append(gr.nulls, isNull)
			allVals = append(allVals, v)
			allNulls = append(allNulls, isNull)
		}
		groups = append(groups, gr)
	}
	seg := buildSeg(t, groups)
	var mn, mx int64
	first := true
	for i, v := range allVals {
		if allNulls[i] {
			continue
		}
		if first || v < mn {
			mn = v
		}
		if first || v > mx {
			mx = v
		}
		first = false
	}
	preds := [][]zone.Pred{
		{zone.Equal(mn)}, {zone.Equal(mx)},
		{zone.GreaterThan(mx)}, {zone.LessThan(mn)},
		{zone.GreaterEqual(mn), zone.LessEqual(mx)}, // tight range
		{zone.GreaterEqual(mx)}, {zone.LessEqual(mn)},
		{zone.Equal(mn + 1), zone.GreaterThan(mn)}, // AND combination
		{zone.InSet(mn, mx, mn-1)},
		{zone.Null()}, {zone.NotNull()},
		{zone.NotNull(), zone.GreaterEqual(mn), zone.LessEqual(mx)},
		{zone.Equal(mn - 1)}, {zone.InSet()}, // empty IN matches nothing
	}
	for _, ps := range preds {
		got := NewScanner(seg, ps).All()
		var want []Row
		for i, v := range allVals {
			if zone.MatchAll(v, allNulls[i], ps) {
				row := Row{Value: v, Null: allNulls[i]}
				if row.Null {
					row.Value = 0 // decoded null slots read back as zero
				}
				want = append(want, row)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("preds %+v: got %d rows, want %d", ps, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("preds %+v row %d: got %+v, want %+v", ps, i, got[i], want[i])
			}
		}
	}
}
