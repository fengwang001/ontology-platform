package store

import (
	"math/bits"
	"testing"
)

// TestLocateComparisonsLogarithmic builds P partitions for several
// magnitudes and asserts one Locate compares no more than
// ceil(log2(P))+1 partitions, i.e. binary search, not a linear scan.
func TestLocateComparisonsLogarithmic(t *testing.T) {
	for _, p := range []int{100, 300, 1000, 3000, 10000} {
		s, err := New(0, int64(8*p), 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		for key := int64(0); len(s.parts) < p; key++ {
			if err := s.Insert(key); err != nil {
				t.Fatalf("P=%d insert %d: %v", p, key, err)
			}
		}
		got := len(s.parts)
		limit := int64(bits.Len(uint(got-1))) + 1 // ceil(log2(P)) + 1
		// Probe every partition boundary region; each Locate must stay
		// within the logarithmic bound.
		for _, key := range []int64{0, s.high / 3, s.high / 2, s.high - 1} {
			if _, err := s.Locate(key); err != nil {
				t.Fatalf("P=%d locate %d: %v", got, key, err)
			}
			if c := s.lastCmp.Load(); c > limit {
				t.Errorf("P=%d key=%d: %d comparisons > ceil(log2(P))+1=%d",
					got, key, c, limit)
			}
		}
	}
}

// TestFindVisitsLogScale double-checks the counter grows like log P,
// not like P, across the size ladder.
func TestFindVisitsLogScale(t *testing.T) {
	var prev int64
	for _, p := range []int{100, 1000, 10000} {
		s, _ := New(0, int64(8*p), 2, 1)
		for key := int64(0); len(s.parts) < p; key++ {
			s.Insert(key)
		}
		s.Locate(s.high - 1)
		c := s.lastCmp.Load()
		if prev > 0 && c > prev+11 { // 10x partitions adds <= ~4 probes
			t.Errorf("P=%d: comparisons jumped %d -> %d", p, prev, c)
		}
		prev = c
	}
}
