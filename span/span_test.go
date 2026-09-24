package span

import (
	"math"
	"testing"
)

func TestBasicMapping(t *testing.T) {
	// original: "ab\r\n c\n"  (indices), CR + leading space deleted
	m := &Map{}
	m.Retain(2) // "ab"
	m.Delete(1) // \r
	m.Retain(1) // \n
	m.Delete(1) // space
	m.Retain(2) // "c\n"
	cases := []struct {
		o, wantOrig int
	}{{0, 0}, {1, 1}, {2, 3}, {3, 5}, {4, 6}, {5, 7}}
	for _, c := range cases {
		if got := m.ToOrig(c.o); got != c.wantOrig {
			t.Fatalf("ToOrig(%d)=%d want %d", c.o, got, c.wantOrig)
		}
		if got := m.ToOut(c.wantOrig); got != c.o {
			t.Fatalf("ToOut(%d)=%d want %d", c.wantOrig, got, c.o)
		}
	}
	// deleted CR at orig 2 and deleted space at orig 4 map to following boundary
	if m.ToOut(2) != 2 || m.ToOut(4) != 3 {
		t.Fatalf("deleted offsets: %d %d", m.ToOut(2), m.ToOut(4))
	}
	if m.Runs() != 5 {
		t.Fatalf("runs=%d", m.Runs())
	}
}

func TestInverseMonotone(t *testing.T) {
	m := &Map{}
	pattern := []struct {
		n int
		d bool
	}{{3, false}, {4, true}, {2, false}, {1, true}, {5, false}}
	for _, p := range pattern {
		if p.d {
			m.Delete(p.n)
		} else {
			m.Retain(p.n)
		}
	}
	prev := -1
	for o := 0; o <= m.OutLen(); o++ {
		i := m.ToOrig(o)
		if m.ToOut(i) != o {
			t.Fatalf("inverse fails at o=%d i=%d", o, i)
		}
		if i < prev {
			t.Fatalf("ToOrig decreases at %d", o)
		}
		prev = i
	}
	prev = -1
	for i := 0; i <= m.OrigLen(); i++ {
		if o := m.ToOut(i); o < prev {
			t.Fatalf("ToOut decreases at %d -> %d", i, o)
		} else {
			prev = o
		}
	}
}

func bound(runs int) int { return 2*int(math.Ceil(math.Log2(float64(runs+1)))) + 4 }

func TestScaleLookup(t *testing.T) {
	sizes := []struct{ lines, bytesPer int }{
		{100_000, 100},
		{200_000, 50}, // ~10M bytes
	}
	for _, s := range sizes {
		m := &Map{}
		for i := 0; i < s.lines; i++ {
			m.Retain(s.bytesPer)
			m.Delete(3) // trailing spaces
			m.Retain(1) // newline
		}
		m.ToOrig(m.OutLen() / 2)
		if got := m.LastLookups(); got > bound(m.Runs()) {
			t.Fatalf("size %v lookups=%d bound=%d", s, got, bound(m.Runs()))
		}
		m.ToOut(m.OrigLen() / 2)
		if got := m.LastLookups(); got > bound(m.Runs()) {
			t.Fatalf("size %v ToOut lookups=%d", s, got)
		}
	}
}

func TestPureNewlinesConstantRuns(t *testing.T) {
	m := &Map{}
	for i := 0; i < 10_000_000; i++ {
		m.Retain(1)
	}
	if m.Runs() != 1 {
		t.Fatalf("pure-newline runs=%d want 1", m.Runs())
	}
	m.ToOrig(5_000_000)
	if got := m.LastLookups(); got > bound(m.Runs()) {
		t.Fatalf("lookups=%d", got)
	}
}

func TestOps(t *testing.T) {
	// "\n x" segment where leading \n is swallowed (cross-cut CRLF)
	m := &Map{}
	m.Retain(3)
	m.DropHeadOne()
	if m.OrigLen() != 3 || m.OutLen() != 2 {
		t.Fatalf("DropHeadOne lens %d %d", m.OrigLen(), m.OutLen())
	}
	if m.ToOrig(0) != 1 {
		t.Fatalf("DropHeadOne ToOrig=%d", m.ToOrig(0))
	}
	// restore trailing deleted run of two spaces
	n := &Map{}
	n.Retain(2)
	n.Delete(2)
	n.KeepTailDropped(2)
	if n.OutLen() != 4 || n.Runs() != 1 {
		t.Fatalf("KeepTailDropped lens/runs %d %d", n.OutLen(), n.Runs())
	}
	// fold "\n\n" tail to one: orig "x\n\n" all retained
	f := &Map{}
	f.Retain(1)
	f.Retain(1)
	f.Retain(1)
	folded := f.FoldTail(1, 2)
	if folded.OutLen() != 2 || folded.ToOrig(1) != 2 {
		t.Fatalf("FoldTail lens/anchor %d %d", folded.OutLen(), folded.ToOrig(1))
	}
}
