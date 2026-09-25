package vcache

import (
	"fmt"
	"testing"

	"ontology/ver"
)

// Invariant: freshness decision is O(1). The number of records traversed to
// decide freshness (scanned) must not grow with the group size m.
func TestFreshnessScanIsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			st := ver.NewStore()
			c := New(st)
			for i := 0; i < m; i++ {
				if err := st.Write("g0", fmt.Sprintf("k%d", i), int64(i)); err != nil {
					t.Fatal(err)
				}
			}
			c.ReadG("g0") // warm the cache
			before := c.scanned
			if err := st.Write("g0", "k0", -1); err != nil { // invalidate
				t.Fatal(err)
			}
			want := int64(m*(m-1)/2) - 1
			if got := c.ReadG("g0"); got != want { // miss -> recompute
				t.Fatalf("ReadG = %d, want %d", got, want)
			}
			if d := c.scanned - before; d > 4 {
				t.Fatalf("freshness check traversed %d records at m=%d; want O(1)", d, m)
			}
		})
	}
}

// Invariant 2: after every read the cache stamp equals the current stamp.
func TestCacheStampCurrent(t *testing.T) {
	steps := []struct {
		write bool
		group string
		key   string
		val   int64
	}{
		{true, "g0", "a", 5}, {false, "g0", "", 0},
		{true, "g0", "b", 3}, {false, "g0", "", 0},
		{true, "g1", "c", 7}, {false, "", "", 0}, // total read
		{true, "g0", "b", 10}, {false, "", "", 0},
	}
	st := ver.NewStore()
	c := New(st)
	for i, s := range steps {
		if s.write {
			if err := st.Write(s.group, s.key, s.val); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if s.group != "" {
			c.ReadG(s.group)
			if e := c.cg[s.group]; e.stamp != st.Stamp(s.group) {
				t.Fatalf("step %d: cacheG stamp %d != current %d", i, e.stamp, st.Stamp(s.group))
			}
		} else {
			c.ReadTotal()
			if c.ct.stamp != st.TotalStamp() {
				t.Fatalf("step %d: cacheT stamp %d != current %d", i, c.ct.stamp, st.TotalStamp())
			}
		}
	}
}

// Invariant 3: every write moves the affected group stamp and the total
// stamp up by exactly 1; nothing ever regresses.
func TestVersionMonotonic(t *testing.T) {
	writes := []struct{ g, k string }{{"g0", "a"}, {"g0", "b"}, {"g1", "c"}, {"g0", "a"}, {"g1", "c"}}
	st := ver.NewStore()
	prev := map[string]int64{}
	var prevTotal int64
	for i, w := range writes {
		if err := st.Write(w.g, w.k, int64(i)); err != nil {
			t.Fatal(err)
		}
		if got := st.Stamp(w.g); got != prev[w.g]+1 {
			t.Fatalf("write %d: stamp(%s) = %d, want %d", i, w.g, got, prev[w.g]+1)
		}
		for _, g := range st.Groups() {
			if st.Stamp(g) < prev[g] {
				t.Fatalf("write %d: stamp(%s) regressed", i, g)
			}
			prev[g] = st.Stamp(g)
		}
		if st.TotalStamp() != prevTotal+1 {
			t.Fatalf("write %d: totalStamp = %d, want %d", i, st.TotalStamp(), prevTotal+1)
		}
		prevTotal = st.TotalStamp()
	}
}
