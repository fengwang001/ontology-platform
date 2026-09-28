package span_test

import (
	"math"
	"testing"

	"ontology/span"
)

func buildMixed() *span.Map {
	m := span.New()
	m.Keep(3)      // "abc"
	m.Delete(3, 5) // two deleted trailing spaces
	m.Keep(1)      // '\n' kept
	m.Delete(6, 7) // lone '\r' part of an ending emulated as delete
	m.Keep(1)
	m.Finish(8)
	return m
}

func TestQueries(t *testing.T) {
	cases := []struct {
		name  string
		build func() *span.Map
	}{
		{"empty", func() *span.Map { m := span.New(); m.Finish(0); return m }},
		{"keptonly", func() *span.Map { m := span.New(); m.Keep(10); m.Finish(10); return m }},
		{"mixed", buildMixed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.build()
			for o := 0; o <= m.OutLen(); o++ {
				i := m.ToOrig(o)
				if got := m.ToOut(i); got != o {
					t.Fatalf("ToOut(ToOrig(%d))=%d want %d", o, got, o)
				}
			}
			prev := -1
			for o := 0; o <= m.OutLen(); o++ {
				if i := m.ToOrig(o); i < prev {
					t.Fatalf("ToOrig not monotone at %d", o)
				} else {
					prev = i
				}
			}
			prev = -1
			for i := 0; i <= m.OrigLen(); i++ {
				if o := m.ToOut(i); o < prev {
					t.Fatalf("ToOut not monotone at %d", i)
				} else {
					prev = o
				}
			}
		})
	}
}

func TestDeletedOffsets(t *testing.T) {
	m := buildMixed()
	// deleted spaces at orig 3,4 map to output point 3 (before kept '\n').
	for _, i := range []int{3, 4} {
		if got := m.ToOut(i); got != 3 {
			t.Fatalf("ToOut(%d)=%d want 3", i, got)
		}
	}
	if m.ToOrig(3) != 5 { // '\n' kept: orig 5
		t.Fatalf("ToOrig(3)=%d want 5", m.ToOrig(3))
	}
	// '\r' deletion at orig 6 maps to output point of following byte.
	if got := m.ToOut(6); got != 4 {
		t.Fatalf("ToOut(6)=%d want 4", got)
	}
}

func TestBinarySearchBound(t *testing.T) {
	const deletions = 200000
	m := span.New()
	for k := 0; k < deletions; k++ {
		m.Keep(1)
		m.Delete(2*k+1, 2*k+2)
	}
	m.Finish(2 * deletions)
	want := 2*math.Log2(float64(len(m.Entries()))) + 4
	for _, o := range []int{0, m.OutLen() / 2, m.OutLen()} {
		m.ToOrig(o)
		if c := m.Checks(); float64(c) > want {
			t.Fatalf("ToOrig checks %d > bound %.1f (entries=%d)", c, want, len(m.Entries()))
		}
	}
	for _, i := range []int{0, m.OrigLen() / 2, m.OrigLen()} {
		m.ToOut(i)
		if c := m.Checks(); float64(c) > want {
			t.Fatalf("ToOut checks %d > bound %.1f", c, want)
		}
	}
}
