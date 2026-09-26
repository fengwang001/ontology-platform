package res

import "testing"

func TestPeakLoad(t *testing.T) {
	cases := []struct {
		name string
		rs   []R
		s, e int64
		want int64
	}{
		{"empty", nil, 0, 10, 0},
		{"single", []R{{0, 5, 4, 1}}, 0, 5, 4},
		{"touching half open", []R{{0, 5, 4, 1}, {5, 9, 7, 2}}, 0, 9, 7},
		{"overlap sums", []R{{0, 5, 4, 1}, {2, 7, 5, 2}}, 0, 9, 9},
		{"window abuts start", []R{{5, 9, 7, 1}}, 0, 5, 0},
		{"window abuts end", []R{{0, 5, 4, 1}}, 5, 9, 0},
		{"nested", []R{{0, 10, 2, 1}, {2, 4, 3, 2}}, 0, 10, 5},
		{"gap picks covered point", []R{{0, 2, 6, 1}, {8, 10, 6, 2}}, 0, 10, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var set Set
			for _, r := range c.rs {
				set.Add(r)
			}
			if got := set.Peak(c.s, c.e); got != c.want {
				t.Fatalf("Peak(%d,%d)=%d want %d", c.s, c.e, got, c.want)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	cases := []struct {
		name     string
		remove   int64
		s, e, pk int64
	}{
		{"remove first", 1, 2, 8, 10},
		{"remove middle", 2, 0, 10, 11},
		{"remove last", 3, 0, 10, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var set Set
			rs := []R{{0, 10, 4, 1}, {2, 8, 3, 2}, {4, 6, 7, 3}}
			for _, r := range rs {
				set.Add(r)
			}
			set.Remove(rs[c.remove-1])
			if got := set.Peak(c.s, c.e); got != c.pk {
				t.Fatalf("after remove %d: Peak=%d want %d", c.remove, got, c.pk)
			}
		})
	}
}

// TestScanCountBounded pins the complexity contract: with m non-overlapping
// reservations, a peak query landing in a gap inspects zero reservations, and
// a query overlapping a fixed small window inspects a constant number of
// endpoints — neither grows with m (ordered-endpoint lookup, not table scan).
func TestScanCountBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		var set Set
		for i := 0; i < m; i++ {
			set.Add(R{int64(4 * i), int64(4*i + 1), 1, int64(i + 1)})
		}
		set.Peak(202, 203) // gap between [200,201) and [204,205)
		if set.scanCount != 0 {
			t.Fatalf("m=%d gap query compared %d reservations, want 0", m, set.scanCount)
		}
		if got := set.Peak(1, 6); got != 1 { // only [4,5) lies inside
			t.Fatalf("m=%d peak=%d want 1", m, got)
		}
		if set.scanCount != 2 { // exactly the two endpoints at 4 and 5
			t.Fatalf("m=%d window query compared %d reservations, want constant 2", m, set.scanCount)
		}
	}
}
