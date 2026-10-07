package bitemporal

import "testing"

// TestCanonicalCover verifies the dyadic partition exactly covers [lo, hi)
// with aligned, non-overlapping nodes including int64 domain extremes.
func TestCanonicalCover(t *testing.T) {
	cases := []struct {
		lo, hi Tick
		n      int
	}{
		{0, 1, 1},
		{0, 1 << 40, 1},
		{3, 11, 4},
		{MinTick, MaxTick, 0}, // full domain; checked structurally below
		{MinTick, MinTick + 1, 1},
	}
	for _, tc := range cases {
		keys := canonicalCover(toU(tc.lo), toU(tc.hi))
		if len(keys) > 2*treeDepth {
			t.Fatalf("cover of [%d,%d) too large: %d", tc.lo, tc.hi, len(keys))
		}
		if tc.n > 0 && len(keys) != tc.n {
			t.Fatalf("cover of [%d,%d): want %d nodes got %d", tc.lo, tc.hi, tc.n, len(keys))
		}
		uLo, uHi := toU(tc.lo), toU(tc.hi)
		var covered, prevEnd uint64
		first := true
		for _, k := range keys {
			size := uint64(1) << (64 - k.level)
			start := k.index * size
			if start < uLo || start > uHi-size || start%size != 0 {
				t.Fatalf("node %+v outside/unaligned for [%d,%d)", k, uLo, uHi)
			}
			if !first && start != prevEnd {
				t.Fatalf("gap/overlap at %d (prevEnd %d)", start, prevEnd)
			}
			first = false
			prevEnd = start + size
			covered += size
		}
		if covered != uHi-uLo || prevEnd != uHi {
			t.Fatalf("cover size %d != interval size %d (end=%d)", covered, uHi-uLo, prevEnd)
		}
	}
}

// TestPointLookupScaleIndependent is the verifiable performance requirement:
// records inspected for one point query must not grow with total history.
// Doubling the object's history leaves the per-query comparison count flat.
func TestPointLookupScaleIndependent(t *testing.T) {
	s := NewStore(0, 0)
	if err := s.RegisterSchema("p", 0, map[string]FieldKind{"v": KindInt}); err != nil {
		t.Fatal(err)
	}
	exp := NewExporter(s, NopLogger{})

	measure := func(hist int) int {
		for i := 0; i < hist; i++ {
			// disjoint intervals across the timeline; every record is history.
			start := Tick(int64(i)*1000 - 5_000_000)
			iv, err := NewInterval(start, start+500)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Write("o", "p", iv, Value{Fields: map[string]any{"v": int64(i)}}); err != nil {
				t.Fatal(err)
			}
			s.AdvanceClock(Tick(hist + i))
		}
		c, err := exp.Freeze(s.Clock())
		if err != nil {
			t.Fatal(err)
		}
		_, inspected := exp.VisibleAt(c, "o", -4_999_750)
		return inspected
	}

	n1 := measure(200)
	n2 := measure(400) // total history triples (200 -> 600 records)
	n3 := measure(800) // total history doubles again (600 -> 1400)
	// Each root-to-leaf path has depth+1 = 64 chains; binary search comparisons
	// per chain are O(log N). The count must not rise linearly: compare growth
	// against a generous theoretical ceiling.
	ceiling := (treeDepth + 1) * 24 // ~ 64 chains * log2 of any realistic N
	t.Logf("inspected records at histories 200/600/1400: %d %d %d (ceiling %d)", n1, n2, n3, ceiling)
	if n1 > ceiling || n2 > ceiling || n3 > ceiling {
		t.Fatalf("point lookup inspected %d/%d/%d > ceiling %d: scales with history", n1, n2, n3, ceiling)
	}
	// Growth from 600 to 1400 records may add at most a couple of comparisons
	// per touched chain (log2(1400/600) ~ 1.2). Allow +10; far below linear
	// growth (which would more than double the count).
	if n3 > n1+10 {
		t.Fatalf("inspected grew %d -> %d despite 7x history: not scale-independent", n1, n3)
	}
}
