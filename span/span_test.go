package span

import (
	"math"
	"testing"
)

func TestIdentityAndGaps(t *testing.T) {
	m := &Map{}
	m.Add(0, 0, 5, 5)
	m.Add(5, 5, 0, 3) // deleted original [5,8)
	m.Add(5, 8, 4, 4)
	cases := []struct {
		o, i int
	}{
		{0, 0}, {4, 4}, {5, 8}, {8, 11}, {9, 12},
	}
	for _, c := range cases {
		if got := m.ToOrig(c.o); got != c.i {
			t.Fatalf("ToOrig(%d)=%d want %d", c.o, got, c.i)
		}
		if got := m.ToOut(c.i); got != c.o {
			t.Fatalf("ToOut(%d)=%d want %d", c.i, got, c.o)
		}
	}
	// Deleted original offsets fold forward onto output point 5.
	for _, i := range []int{5, 6, 7} {
		if got := m.ToOut(i); got != 5 {
			t.Fatalf("ToOut(deleted %d)=%d want 5", i, got)
		}
	}
}

func TestMonotone(t *testing.T) {
	m := &Map{}
	o := 0
	for i := 0; i < 300; i++ {
		if i%3 == 2 {
			m.Add(o, i, 0, 1) // deleted original byte
		} else {
			m.Add(o, i, 1, 1)
			o++
		}
	}
	prev := -1
	for i := 0; i <= m.LenOrig(); i++ {
		got := m.ToOut(i)
		if got < prev {
			t.Fatalf("ToOut not monotone at %d", i)
		}
		prev = got
	}
	prev = -1
	for o := 0; o <= m.LenOut(); o++ {
		got := m.ToOrig(o)
		if got < prev {
			t.Fatalf("ToOrig not monotone at %d", o)
		}
		prev = got
		if m.ToOut(got) != o {
			t.Fatalf("ToOut(ToOrig(%d))=%d", o, m.ToOut(got))
		}
	}
}

func TestChecksBound(t *testing.T) {
	m := &Map{}
	pos := 0
	for k := 0; k < 200000; k++ { // one deletion point per line
		m.Add(pos, k*6, 5, 5)
		m.Add(pos+5, k*6+5, 0, 1)
		pos += 5
	}
	n := len(m.Segs())
	bound := int(2*math.Log2(float64(n)) + 4)
	for _, q := range []int{0, pos / 2, pos - 1, pos} {
		m.ToOrig(q)
		if m.Checks() > bound {
			t.Fatalf("ToOrig checks %d > %d (segs %d)", m.Checks(), bound, n)
		}
		m.ToOut(m.LenOrig() * q / pos)
		if m.Checks() > bound {
			t.Fatalf("ToOut checks %d > %d (segs %d)", m.Checks(), bound, n)
		}
	}
}

func TestPureNewlinesCoalesce(t *testing.T) {
	m := &Map{}
	for k := 0; k < 10_000_000; k++ {
		m.Add(k, k, 1, 1)
	}
	if len(m.Segs()) > 1 {
		t.Fatalf("identity run produced %d segments, want <=1", len(m.Segs()))
	}
}
