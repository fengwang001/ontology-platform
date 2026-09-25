package span_test

import (
	"math"
	"testing"

	"ontology/span"
)

func TestBasicMap(t *testing.T) {
	cases := []struct {
		name       string
		build      func() *span.Map
		orig, out  int64
		toOrig     map[int64]int64
		toOut      map[int64]int64
		intervals  int
	}{
		{
			name: "survive",
			build: func() *span.Map {
				m := span.New()
				m.Survive(10)
				return m
			},
			orig: 10, out: 10, intervals: 1,
			toOrig: map[int64]int64{0: 0, 5: 5, 10: 10},
			toOut:  map[int64]int64{0: 0, 5: 5, 10: 10},
		},
		{
			name: "crlf_and_ws", // original "a \r\nb": del spaces run+CR between
			build: func() *span.Map {
				m := span.New()
				m.Survive(1) // a
				m.Delete(2)  // space + CR
				m.Survive(1) // LF -> out \n
				m.Survive(1) // b
				return m
			},
			orig: 5, out: 3, intervals: 3,
			toOrig: map[int64]int64{0: 0, 1: 3, 2: 3, 3: 4},
			toOut:  map[int64]int64{0: 0, 1: 1, 2: 1, 3: 1, 4: 2, 5: 3},
		},
		{
			name: "append_newline",
			build: func() *span.Map {
				m := span.New()
				m.Survive(2)
				m.Append(1)
				return m
			},
			orig: 2, out: 3, intervals: 2,
			toOrig: map[int64]int64{0: 0, 1: 1, 2: 2},
			toOut:  map[int64]int64{0: 0, 1: 1, 2: 3},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := c.build()
			if m.OrigLen() != c.orig || m.OutLen() != c.out || m.Intervals() != c.intervals {
				t.Fatalf("len=%d/%d intervals=%d want %d/%d %d", m.OrigLen(), m.OutLen(), m.Intervals(), c.orig, c.out, c.intervals)
			}
			for o, want := range c.toOrig {
				if got := m.ToOrig(o); got != want {
					t.Errorf("ToOrig(%d)=%d want %d", o, got, want)
				}
				if got := m.ToOut(m.ToOrig(o)); got != o {
					t.Errorf("roundtrip at out %d: %d", o, got)
				}
			}
			for i, want := range c.toOut {
				if got := m.ToOut(i); got != want {
					t.Errorf("ToOut(%d)=%d want %d", i, got, want)
				}
			}
		})
	}
}

func TestMonotonic(t *testing.T) {
	m := span.New()
	m.Survive(4)
	m.Delete(3)
	m.Survive(5)
	m.Append(2)
	m.Survive(2)
	var prev int64 = -1
	for o := int64(0); o <= m.OutLen(); o++ {
		if got := m.ToOrig(o); got < prev {
			t.Fatalf("ToOrig not monotone at %d", o)
		} else {
			prev = got
		}
	}
	prev = -1
	for i := int64(0); i <= m.OrigLen(); i++ {
		if got := m.ToOut(i); got < prev {
			t.Fatalf("ToOut not monotone at %d", i)
		} else {
			prev = got
		}
	}
}

func deletionsMap(lines int) *span.Map {
	m := span.New()
	for j := 0; j < lines; j++ {
		m.Survive(int64(2 + j%5))
		m.Delete(int64(1 + j%3)) // trailing ws + CR
		m.Survive(1)             // LF
	}
	return m
}

func TestProbeBound(t *testing.T) {
	sizes := []struct{ name string; lines int }{
		{"100k_lines", 100000},
		{"10MB", 10 * 1024 * 1024 / 20},
	}
	for _, s := range sizes {
		t.Run(s.name, func(t *testing.T) {
			m := deletionsMap(s.lines)
			bound := 2*math.Log2(float64(m.Intervals())) + 4
			midOut := m.OutLen() / 2
			m.ToOrig(midOut)
			if float64(m.Probes()) > bound {
				t.Fatalf("ToOrig probes=%d bound=%.1f intervals=%d", m.Probes(), bound, m.Intervals())
			}
			m.ToOut(m.OrigLen() / 2)
			if float64(m.Probes()) > bound {
				t.Fatalf("ToOut probes=%d bound=%.1f", m.Probes(), bound)
			}
			t.Logf("intervals=%d probes=%d bound=%.1f", m.Intervals(), m.Probes(), bound)
		})
	}
}

func TestPureNewlinesConstantIntervals(t *testing.T) {
	m := span.New()
	for i := 0; i < 10*1024*1024; i++ {
		m.Survive(1)
	}
	if m.Intervals() > 2 {
		t.Fatalf("intervals=%d, want <= 2", m.Intervals())
	}
	m2 := span.New()
	m2.Survive(int64(10 * 1024 * 1024))
	if m2.Intervals() != 1 {
		t.Fatalf("bulk intervals=%d", m2.Intervals())
	}
}

func TestConcatAndTruncate(t *testing.T) {
	a := span.New()
	a.Survive(3)
	a.Delete(1)
	a.Survive(1)
	b := span.New()
	b.Survive(2)
	a.Concat(b)
	if a.OrigLen() != 7 || a.OutLen() != 6 || a.Intervals() != 3 {
		t.Fatalf("concat got %d/%d/%d", a.OrigLen(), a.OutLen(), a.Intervals())
	}
	a.TruncateOut(5)
	if a.OutLen() != 5 {
		t.Fatalf("truncate out=%d", a.OutLen())
	}
}
