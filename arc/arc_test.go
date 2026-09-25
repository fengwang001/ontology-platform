package arc

import "testing"

// TestEvictInspectBound asserts one Evict inspects at most k+1 entries
// when it evicts exactly k from the head. It reads the unexported counter
// directly (white-box): no exported method may expose it.
func TestEvictInspectBound(t *testing.T) {
	cases := []struct {
		m, k int
	}{
		{100, 1}, {100, 25}, {100, 99},
		{1000, 7}, {1000, 500}, {1000, 1000},
		{10000, 3}, {10000, 1234}, {10000, 9999},
	}
	for _, c := range cases {
		l := New()
		for i := 0; i < c.m; i++ { // ts strictly increasing 0..m-1
			l.Append(int64(i+1), int64(i))
		}
		l.Evict(int64(c.k)) // exactly entries ts<k (=k entries) expire
		if l.checked > c.k+1 {
			t.Fatalf("m=%d k=%d: inspected %d entries, want <= %d (not full scan)",
				c.m, c.k, l.checked, c.k+1)
		}
		if got := l.Len(); got != c.m-c.k {
			t.Fatalf("m=%d k=%d: surviving len=%d want %d", c.m, c.k, got, c.m-c.k)
		}
		if c.k < c.m {
			if m, ok := l.Max(); !ok || m != int64(c.m) {
				t.Fatalf("m=%d k=%d: max=%d,ok=%v want %d,true", c.m, c.k, m, ok, c.m)
			}
		} else if _, ok := l.Max(); ok {
			t.Fatalf("fully evicted archive should report empty")
		}
	}
}

// TestEvictHeadAndMax covers strict boundary, monotone cutoff walk and the
// empty-archive Max, table-driven.
func TestEvictHeadAndMax(t *testing.T) {
	cases := []struct {
		name   string
		app    [][2]int64
		cutoff int64
		want   []int64 // surviving offsets, in order
		maxOK  bool
	}{
		{"empty", nil, 5, nil, false},
		{"strictBoundaryKeepsEqual", [][2]int64{{110, 10}}, 10, []int64{110}, true},
		{"dropHeadKeepTail", [][2]int64{{1, 0}, {2, 5}, {3, 10}}, 1, []int64{2, 3}, true},
		{"dropAll", [][2]int64{{1, 0}, {2, 1}}, 5, nil, false},
		{"keepAll", [][2]int64{{1, 10}, {2, 20}}, 5, []int64{1, 2}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := New()
			for _, a := range c.app {
				l.Append(a[0], a[1])
			}
			l.Evict(c.cutoff)
			if l.Len() != len(c.want) {
				t.Fatalf("len=%d want %d", l.Len(), len(c.want))
			}
			for i, w := range c.want {
				if got := l.e[i].off; got != w {
					t.Fatalf("entry %d off=%d want %d", i, got, w)
				}
			}
			m, ok := l.Max()
			if ok != c.maxOK {
				t.Fatalf("Max ok=%v want %v", ok, c.maxOK)
			}
			if c.maxOK && m != c.want[len(c.want)-1] {
				t.Fatalf("Max=%d want %d", m, c.want[len(c.want)-1])
			}
		})
	}
}
