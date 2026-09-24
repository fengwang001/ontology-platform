package chain

import "testing"

func TestVisible(t *testing.T) {
	c := New()
	c.Prepend("v1", 1)
	c.Prepend("v2", 2)
	c.Prepend("v3", 3)
	cases := []struct {
		s    int64
		want string
		ok   bool
	}{
		{0, "", false}, {-1, "", false}, {1, "v1", true},
		{2, "v2", true}, {3, "v3", true}, {99, "v3", true},
	}
	for _, tc := range cases {
		got, ok := c.Visible(tc.s)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("Visible(%d)=(%q,%v), want (%q,%v)", tc.s, got, ok, tc.want, tc.ok)
		}
	}
	if vers := c.Vers(); len(vers) != 3 || vers[0] != 3 || vers[2] != 1 {
		t.Fatalf("Vers newest-first = %v", vers)
	}
}

// TestComparisonSublinear proves logarithmic visibility lookup: as the chain
// grows to 10k versions, comparisons made by Visible stay logarithmic and do
// not grow linearly with m. The counter is unexported and read in-package.
func TestComparisonSublinear(t *testing.T) {
	sizes := []int{100, 1000, 10000}
	var prev, first int64
	for gi, m := range sizes {
		c := New()
		for v := 1; v <= m; v++ {
			c.Prepend("v", int64(v))
		}
		s := int64(m / 2) // read point in the middle of the chain
		if _, ok := c.Visible(s); !ok {
			t.Fatalf("m=%d: mid-snapshot not found", m)
		}
		if c.cmps.Load() > 20 { // ceil(log2(10000)) ~ 14; a linear scan would be ~5000
			t.Fatalf("m=%d: %d comparisons, expected <= 20 (binary search)", m, c.cmps.Load())
		}
		if gi == 0 {
			first = c.cmps.Load()
		} else if c.cmps.Load() > first+8 { // 100x more nodes may add at most log2(100) ~ 7 probes
			t.Fatalf("comparisons grew linearly: m=100 -> %d, m=%d -> %d", first, m, c.cmps.Load())
		}
		prev = c.cmps.Load()
	}
	if prev >= 1000 {
		t.Fatalf("comparisons %d look linear, not logarithmic", prev)
	}
}

func TestCollectIntervals(t *testing.T) {
	// Chain [3,2,1]; active snapshot 2 only: v1 interval [1,2) empty -> drop,
	// v2 interval [2,3) holds 2 -> keep, head 3 kept.
	c := New()
	c.Prepend("v1", 1)
	c.Prepend("v2", 2)
	c.Prepend("v3", 3)
	active := map[int64]bool{2: true}
	n := c.Collect(func(v, vp int64) bool {
		for _, a := range mapKeys(active) {
			if v <= a && a < vp {
				return true
			}
		}
		return false
	})
	if n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if vers := c.Vers(); len(vers) != 2 || vers[0] != 3 || vers[1] != 2 {
		t.Fatalf("chain after collect = %v, want [3 2]", vers)
	}
	if v, ok := c.Visible(2); !ok || v != "v2" {
		t.Fatalf("Visible(2)=(%q,%v), want v2", v, ok)
	}
}

func mapKeys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
