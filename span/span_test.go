package span

import (
	"math"
	"testing"
)

// runs: [0,2)->out[0,2), gap [2,4), [4,5)->out[2,3), tail [5,6) deleted.
func testMap() *Map {
	var b Builder
	b.Keep(0, 2, 0)
	b.Keep(4, 5, 2)
	return b.Build(6)
}

func TestToOut(t *testing.T) {
	m := testMap()
	cases := []struct{ in, want int }{
		{0, 0}, {1, 1}, // retained
		{2, 2}, {3, 2}, // deleted gap maps forward
		{4, 2}, // retained
		{5, 3}, // deleted tail maps to outLen
		{6, 3}, // endpoint
	}
	for _, c := range cases {
		if got := m.ToOut(c.in); got != c.want {
			t.Errorf("ToOut(%d)=%d want %d", c.in, got, c.want)
		}
	}
}

func TestToOrig(t *testing.T) {
	m := testMap()
	cases := []struct{ in, want int }{
		{0, 0}, {1, 1}, {2, 4}, {3, 6}, // endpoint
	}
	for _, c := range cases {
		if got := m.ToOrig(c.in); got != c.want {
			t.Errorf("ToOrig(%d)=%d want %d", c.in, got, c.want)
		}
	}
	for o := 0; o <= m.OutLen(); o++ {
		if got := m.ToOut(m.ToOrig(o)); got != o {
			t.Errorf("ToOut(ToOrig(%d))=%d", o, got)
		}
	}
}

func TestMonotonic(t *testing.T) {
	m := testMap()
	for i := 0; i < m.OrigLen(); i++ {
		if m.ToOut(i) > m.ToOut(i+1) {
			t.Fatalf("ToOut not monotonic at %d", i)
		}
		if m.ToOrig(i) > m.ToOrig(i+1) {
			t.Fatalf("ToOrig not monotonic at %d", i)
		}
	}
}

func TestCheckedBound(t *testing.T) {
	for _, n := range []int{1, 7, 1000, 100000} {
		var b Builder
		for i := 0; i < n; i++ {
			b.Keep(2*i, 2*i+1, i) // every other byte deleted: n runs
		}
		m := b.Build(2 * n)
		bound := 2*int(math.Log2(float64(n))) + 4
		m.ToOrig(n - 1)
		if m.LastChecked() > bound {
			t.Errorf("ToOrig checked %d runs, bound %d (n=%d)", m.LastChecked(), bound, n)
		}
		m.ToOut(2*n - 1)
		if m.LastChecked() > bound {
			t.Errorf("ToOut checked %d runs, bound %d (n=%d)", m.LastChecked(), bound, n)
		}
	}
}

func TestBuilderMerge(t *testing.T) {
	var b Builder
	b.Keep(0, 1, 0)
	b.Keep(1, 3, 1) // adjacent: merges into one run
	if m := b.Build(3); m.Len() != 1 {
		t.Fatalf("runs=%d want 1", m.Len())
	}
}
