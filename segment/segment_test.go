package segment

import (
	"math"
	"math/rand"
	"testing"
)

func buildSegment(t *testing.T, cfg Config, groups []struct {
	vals  []int64
	nulls []bool
}) *Segment {
	t.Helper()
	b := NewBuilder(cfg)
	for _, g := range groups {
		if err := b.AddRowGroup(g.vals, g.nulls); err != nil {
			t.Fatalf("add group: %v", err)
		}
	}
	seg, err := Open(b.Bytes())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return seg
}

func group(vals ...int64) struct {
	vals  []int64
	nulls []bool
} {
	return struct {
		vals  []int64
		nulls []bool
	}{vals, make([]bool, len(vals))}
}

func checkGroup(t *testing.T, seg *Segment, i int, vals []int64, nulls []bool) {
	t.Helper()
	gotV, gotN, err := seg.DecodeRowGroup(i)
	if err != nil {
		t.Fatalf("decode group %d: %v", i, err)
	}
	if len(gotV) != len(vals) || len(gotN) != len(nulls) {
		t.Fatalf("group %d: decoded %d rows, want %d", i, len(gotV), len(vals))
	}
	for j := range vals {
		if gotN[j] != nulls[j] {
			t.Fatalf("group %d row %d: null=%v, want %v", i, j, gotN[j], nulls[j])
		}
		if !nulls[j] && gotV[j] != vals[j] {
			t.Fatalf("group %d row %d: value=%d, want %d", i, j, gotV[j], vals[j])
		}
	}
}

func TestRoundTripBothEncodings(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var groups []struct {
		vals  []int64
		nulls []bool
	}
	// Dict-friendly group (low cardinality).
	dg := group(5, 5, -2, 5, -2, 0, 0, 5)
	dg.nulls[3] = true
	groups = append(groups, dg)
	// High-cardinality group forces bit-packing (MaxDictCard=4).
	bg := group(math.MinInt64, math.MaxInt64, 0, -1, 1, 12345, -99999, 42)
	bg.nulls[0] = true
	groups = append(groups, bg)
	// Random group with random nulls.
	rg := group()
	for i := 0; i < 100; i++ {
		rg.vals = append(rg.vals, rng.Int63n(2000)-1000)
		rg.nulls = append(rg.nulls, rng.Intn(4) == 0)
	}
	groups = append(groups, rg)
	// All-equal and all-null groups.
	groups = append(groups, group(9, 9, 9, 9, 9))
	allNull := group(0, 0, 0)
	for i := range allNull.nulls {
		allNull.nulls[i] = true
	}
	groups = append(groups, allNull)

	seg := buildSegment(t, Config{MaxDictCard: 4}, groups)
	if seg.GroupEncoding(0) != DictEncoded {
		t.Fatalf("group 0 encoding %v, want dict", seg.GroupEncoding(0))
	}
	if seg.GroupEncoding(1) != BitPacked {
		t.Fatalf("group 1 encoding %v, want bitpack", seg.GroupEncoding(1))
	}
	for i, g := range groups {
		checkGroup(t, seg, i, g.vals, g.nulls)
	}
}

func TestNullZeroDistinct(t *testing.T) {
	g := group(0, 0, 1)
	g.nulls[1] = true // row 1 is null, row 0 is a real zero
	seg := buildSegment(t, Config{}, []struct {
		vals  []int64
		nulls []bool
	}{g})
	vals, nulls, err := seg.DecodeRowGroup(0)
	if err != nil {
		t.Fatal(err)
	}
	if nulls[0] || vals[0] != 0 {
		t.Fatal("row 0 must be a real zero")
	}
	if !nulls[1] {
		t.Fatal("row 1 must be null, distinct from zero")
	}
	st := seg.GroupStats(0)
	if st.Min != 0 || st.Max != 1 || st.Nulls != 1 {
		t.Fatalf("stats %+v must exclude the null", st)
	}
}

func TestAllNullGroupStats(t *testing.T) {
	g := group(0, 0, 0)
	for i := range g.nulls {
		g.nulls[i] = true
	}
	seg := buildSegment(t, Config{}, []struct {
		vals  []int64
		nulls []bool
	}{g})
	st := seg.GroupStats(0)
	if st.HasMinMax {
		t.Fatalf("all-null group must have no min/max, got %+v", st)
	}
	if st.Nulls != 3 || st.Rows != 3 {
		t.Fatalf("stats %+v", st)
	}
}
