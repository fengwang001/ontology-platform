package span

import (
	"math"
	"testing"
)

func TestMapQueries(t *testing.T) {
	// original: "ab   \nx" positions a0 b1 sp2 sp3 sp4 \n5 x6
	m := &Map{}
	m.Add(0, 0, 2) // "ab"
	m.Add(2, 5, 1) // '\n'
	m.Add(3, 6, 1) // 'x'
	cases := []struct {
		name    string
		o, orig     int64
	}{
		{"start", 0, 0},
		{"ab", 1, 1},
		{"newline", 2, 5},
		{"x", 3, 6},
		{"end", 4, 7},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := m.ToOrig(c.o); got != c.orig {
				t.Fatalf("ToOrig(%d)=%d want %d", c.o, got, c.orig)
			}
			if got := m.ToOut(c.orig); got != c.o {
				t.Fatalf("ToOut(%d)=%d want %d", c.orig, got, c.o)
			}
			if got := m.ToOut(m.ToOrig(c.o)); got != c.o {
				t.Fatalf("roundtrip at %d -> %d", c.o, got)
			}
		})
	}
	// deleted spaces at original offsets 2,3,4 map to the following '\n'
	for _, i := range []int64{2, 3, 4} {
		if got := m.ToOut(i); got != 2 {
			t.Fatalf("deleted ToOut(%d)=%d want 2", i, got)
		}
	}
	if m.Len() != 2 { // "ab" then contiguous "\nx"
		t.Fatalf("segments=%d want 2", m.Len())
	}
}

func TestCoalesceAndMerge(t *testing.T) {
	m := &Map{}
	for k := 0; k < 1000; k++ {
		m.Add(int64(k), int64(k), 1)
	}
	if m.Len() != 1 {
		t.Fatalf("pure-keep segments=%d, want 1", m.Len())
	}
	a, b := &Map{}, &Map{}
	a.Add(0, 0, 5)
	b.Add(0, 100, 3)
	a.Merge(b, 5, 10)
	if a.Len() != 2 {
		t.Fatalf("merge segs=%d want 2", a.Len())
	}
	if a.ToOrig(7) != 112 || a.ToOut(112) != 7 {
		t.Fatalf("shifted mapping wrong")
	}
	r := a.Truncate(6)
	if got := r.ToOrig(6); got != 10 {
		t.Fatalf("truncate endpoint=%d want 10", got)
	}
}

func TestBinarySearchBound(t *testing.T) {
	// build a map with many deletion points: keep1, delete1, keep1...
	m := &Map{}
	const deletions = 100000
	var o, i int64
	for k := 0; k < deletions; k++ {
		m.Add(o, i, 1)
		o++
		i += 2 // one kept, one deleted
	}
	bound := 2*math.Log2(float64(m.Len())) + 4
	worst := 0
	for k := int64(0); k <= o; k++ {
		m.ToOrig(k)
		if m.Checked() > worst {
			worst = m.Checked()
		}
		m.ToOut(2 * k)
		if m.Checked() > worst {
			worst = m.Checked()
		}
	}
	if float64(worst) > bound {
		t.Fatalf("checked=%d > bound %.1f (segs=%d)", worst, bound, m.Len())
	}
}
