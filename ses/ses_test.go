package ses

import (
	"errors"
	"fmt"
	"testing"
)

// TestRouteConstantChecks proves routing locates a key directly: the
// number of (replica, key) entries a Read inspects is a small constant
// that does not grow with the number of keys m on the primary.
func TestRouteConstantChecks(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			c := NewCluster()
			s := c.Open()
			for i := 0; i < m; i++ {
				if _, err := s.Write(fmt.Sprintf("k%d", i), "v"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Write("target", "x"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Read("target"); err != nil {
				t.Fatal(err)
			}
			if s.checked > 2 {
				t.Fatalf("m=%d: routing inspected %d entries, want <= 2", m, s.checked)
			}
		})
	}
}

// TestSyncNoRegression: invariant 3 — follower versions never go
// backwards, across interleaved writes and syncs.
func TestSyncNoRegression(t *testing.T) {
	c := NewCluster()
	s := c.Open()
	steps := []struct {
		key, val string
		sync     int // 0 = no sync after this write
	}{
		{"a", "1", 1}, {"a", "2", 2}, {"b", "x", 1},
		{"a", "3", 0}, {"b", "y", 2}, {"c", "z", 1},
	}
	prev := [2]map[string]int64{{}, {}}
	for _, st := range steps {
		if _, err := s.Write(st.key, st.val); err != nil {
			t.Fatal(err)
		}
		if st.sync == 0 {
			continue
		}
		if err := c.Sync(st.sync); err != nil {
			t.Fatal(err)
		}
		snap := c.Snapshot()
		for i := 0; i < 2; i++ {
			for k, e := range snap[i+1] {
				if prev[i][k] > e.Ver {
					t.Fatalf("follower %d key %s: %d -> %d", i+1, k, prev[i][k], e.Ver)
				}
			}
			prev[i] = map[string]int64{}
			for k, e := range snap[i+1] {
				prev[i][k] = e.Ver
			}
		}
	}
}

// TestReadRouting: read-your-writes routing against the sticky follower.
func TestReadRouting(t *testing.T) {
	c := NewCluster()
	s := c.Open()
	steps := []struct {
		op   func() error
		want string
		ver  int64
	}{
		{func() error { _, e := s.Write("k", "a"); return e }, "a", 1}, // R1 stale -> R0
		{func() error { return c.Sync(1) }, "a", 1},                    // R1 serves
		{func() error { _, e := s.Write("k", "b"); return e }, "b", 2}, // fallback again
	}
	for i, st := range steps {
		if err := st.op(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		got, err := s.Read("k")
		if err != nil || got.Val != st.want || got.Ver != st.ver {
			t.Fatalf("step %d: read (%s,%d,%v), want (%s,%d)", i, got.Val, got.Ver, err, st.want, st.ver)
		}
	}
	s2 := c.Open() // sessions' writeVer maps are independent
	if _, err := s2.Write("k", "other"); err != nil {
		t.Fatal(err)
	}
	if s.writeVer["k"] != 2 || s2.writeVer["k"] != 3 {
		t.Fatalf("writeVer leaked across sessions: s=%d s2=%d, want 2 and 3",
			s.writeVer["k"], s2.writeVer["k"])
	}
}

// TestSyncBadIndex: out-of-range follower indices are rejected, no trace.
func TestSyncBadIndex(t *testing.T) {
	c := NewCluster()
	s := c.Open()
	s.Write("k", "a")
	before := c.Snapshot()
	for _, idx := range []int{-1, 0, 3, 100} {
		if err := c.Sync(idx); !errors.Is(err, ErrBadIndex) {
			t.Fatalf("Sync(%d) = %v, want ErrBadIndex", idx, err)
		}
	}
	after := c.Snapshot()
	for i := 0; i < 3; i++ {
		if len(after[i]) != len(before[i]) {
			t.Fatalf("replica %d changed by rejected Sync", i)
		}
	}
}
