package span_test

import (
	"math"
	"testing"

	"ontology/span"
)

func TestMapping(t *testing.T) {
	// "ab  x\r\nc" -> "ab  x\nc": \r deleted at [5,6)->[5,5)
	var m span.Map
	m.Add(0, 5, 0, 5)
	m.Add(5, 6, 5, 5)
	m.Add(6, 8, 5, 7)
	m.Finish(8, 7)
	origs := []struct {
		o, want int
	}{{0, 0}, {4, 4}, {5, 6}, {6, 7}, {7, 8}}
	outs := []struct {
		i, want int
	}{{0, 0}, {4, 4}, {5, 5}, {6, 5}, {7, 6}, {8, 7}}
	for _, c := range origs {
		if got := m.ToOrig(c.o); got != c.want {
			t.Fatalf("ToOrig(%d)=%d want %d", c.o, got, c.want)
		}
		if got := m.ToOut(m.ToOrig(c.o)); got != c.o {
			t.Fatalf("inverse fails at %d -> %d", c.o, got)
		}
	}
	for _, c := range outs {
		if got := m.ToOut(c.i); got != c.want {
			t.Fatalf("ToOut(%d)=%d want %d", c.i, got, c.want)
		}
	}
	prev := -1
	for i := 0; i <= 8; i++ {
		if got := m.ToOut(i); got < prev {
			t.Fatalf("ToOut not monotone at %d", i)
		}
		prev = m.ToOut(i)
	}
}

func TestProbeBoundAndSegCount(t *testing.T) {
	cases := []struct {
		name       string
		lines      int
		plainBytes int
	}{
		{"100k-lines", 100_000, 0},
		{"10MB-plain", 0, 10_000_000},
	}
	for _, c := range cases {
		var m span.Map
		if c.lines > 0 {
			for l := 0; l < c.lines; l++ { // per line: 1 kept byte, " \t", \n
				m.Add(l*4, l*4+1, l*2, l*2+1)
				m.Add(l*4+1, l*4+3, l*2+1, l*2+1)
				m.Add(l*4+3, l*4+4, l*2+1, l*2+2)
			}
			m.Finish(c.lines*4, c.lines*2)
		} else {
			m.Add(0, c.plainBytes, 0, c.plainBytes)
			m.Finish(c.plainBytes, c.plainBytes)
			if n := m.Len(); n > 4 {
				t.Fatalf("%s: identity text produced %d segments", c.name, n)
			}
		}
		segCount := m.Len()
		bound := int(math.Ceil(math.Log2(float64(segCount+1)))) + 2
		origEnd, outEnd := m.Sizes()
		for _, o := range []int{0, outEnd / 2, outEnd - 1, outEnd} {
			_ = m.ToOrig(o)
			if p := m.Probes(); p > bound {
				t.Fatalf("%s ToOrig probes=%d > %d (segs=%d)", c.name, p, bound, segCount)
			}
		}
		for _, i := range []int{0, origEnd / 2, origEnd - 1, origEnd} {
			_ = m.ToOut(i)
			if p := m.Probes(); p > bound {
				t.Fatalf("%s ToOut probes=%d > %d (segs=%d)", c.name, p, bound, segCount)
			}
		}
	}
}

func TestFoldTail(t *testing.T) {
	cases := []struct {
		k    int
		want int
	}{{1, 2}, {0, 1}, {3, 4}}
	for _, c := range cases {
		var m span.Map
		m.Add(0, 4, 0, 4) // "a\n\n\n"
		m.Finish(4, 4)
		if got := m.FoldTail(c.k, []int{1, 2, 3}, []int{1, 2, 3}); got != c.want {
			t.Fatalf("k=%d out=%d want %d", c.k, got, c.want)
		}
	}
}
