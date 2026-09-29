package span

import (
	"math/bits"
	"testing"
)

func TestMapQueries(t *testing.T) {
	// orig: "ab \tXY\r\nz" -> out "abXY\nz" ; delete [2,4) ws, [6,7) CR
	b := &Builder{}
	b.Add(Seg{0, 2, 0, 2}) // ab
	b.Add(Seg{2, 4, 2, 2}) // " \t" deleted
	b.Add(Seg{4, 6, 2, 4}) // XY
	b.Add(Seg{6, 7, 4, 4}) // \r deleted
	b.Add(Seg{7, 8, 4, 5}) // \n -> \n
	b.Add(Seg{8, 9, 5, 6}) // z
	m := b.Build(9, 6)

	wantOrig := []int{0, 1, 4, 5, 8, 9} // o=0..5
	for o, want := range wantOrig {
		if got := m.ToOrig(o); got != want {
			t.Fatalf("ToOrig(%d)=%d want %d", o, got, want)
		}
		if got := m.ToOut(want); got != o {
			t.Fatalf("ToOut(ToOrig(%d))=%d want %d", o, got, o)
		}
	}
	if m.ToOrig(6) != 9 {
		t.Fatalf("ToOrig(end)=%d", m.ToOrig(6))
	}
	// deleted whitespace and CR map to output point before the deletion
	for _, i := range []int{2, 3} {
		if m.ToOut(i) != 2 {
			t.Fatalf("ToOut(ws %d)=%d want 2", i, m.ToOut(i))
		}
	}
	if m.ToOut(6) != 4 {
		t.Fatalf("ToOut(CR)=%d want 4", m.ToOut(6))
	}
	if m.ToOut(9) != 6 {
		t.Fatalf("ToOut(end)=%d", m.ToOut(9))
	}
	// monotonicity over full domains
	prev := -1
	for o := 0; o <= 6; o++ {
		if v := m.ToOrig(o); v < prev {
			t.Fatalf("ToOrig not monotone at %d", o)
		} else {
			prev = v
		}
	}
	prev = -1
	for i := 0; i <= 9; i++ {
		if v := m.ToOut(i); v < prev {
			t.Fatalf("ToOut not monotone at %d", i)
		} else {
			prev = v
		}
	}
}

func TestInsertionAndIdentity(t *testing.T) {
	cases := []struct {
		name       string
		segs       []Seg
		ilen, olen int
		wantToOrig []int
	}{
		{"identity-only", nil, 3, 3, []int{0, 1, 2, 3}},
		// "ab" + appended "\n": insertion [2,2)->[2,3)
		{"append-lf", []Seg{{0, 2, 0, 2}, {2, 2, 2, 3}}, 2, 3,
			[]int{0, 1, 2, 2}},
		// leading delete: orig " x" -> out "x"
		{"leading-del", []Seg{{0, 1, 0, 0}, {1, 2, 0, 1}}, 2, 1,
			[]int{1, 2}},
	}
	for _, c := range cases {
		b := &Builder{}
		for _, s := range c.segs {
			b.Add(s)
		}
		m := b.Build(c.ilen, c.olen)
		for o, want := range c.wantToOrig {
			if got := m.ToOrig(o); got != want {
				t.Fatalf("%s ToOrig(%d)=%d want %d", c.name, o, got, want)
			}
			if got := m.ToOut(want); got != o {
				t.Fatalf("%s roundtrip o=%d -> %d", c.name, o, got)
			}
		}
	}
}

func TestQueryComplexity(t *testing.T) {
	// 100k lines, mixed endings and trailing whitespace: 3 non-identity
	// segments per line (ws delete, maybe CR delete).
	const lines = 100000
	b := &Builder{}
	var io, oo int
	for l := 0; l < lines; l++ {
		b.Add(Seg{io, io + 1, oo, oo + 1}) // one content byte
		io++
		oo++
		b.Add(Seg{io, io + 2, oo, oo}) // two trailing spaces
		io += 2
		if l%2 == 0 { // CRLF half of the time
			b.Add(Seg{io, io + 1, oo, oo})
			io++
		}
		b.Add(Seg{io, io + 1, oo, oo + 1}) // \n (or \r)
		io++
		oo++
	}
	m := b.Build(io, oo)
	bound := 2*bits.Len(uint(m.Count())) + 4
	for _, o := range []int{0, oo / 2, oo - 1, oo} {
		m.ToOrig(o)
		if m.Checks() > bound {
			t.Fatalf("ToOrig checks %d > %d", m.Checks(), bound)
		}
		m.ToOut(m.ToOrig(o))
		if m.Checks() > bound {
			t.Fatalf("ToOut checks %d > %d", m.Checks(), bound)
		}
	}
}

func TestIntervalCountIndependentOfOutputBytes(t *testing.T) {
	// 10 MB of pure '\n' text: nothing to delete or insert -> 0 intervals.
	const n = 10_000_000
	m := (&Builder{}).Build(n, n)
	if m.Count() != 0 {
		t.Fatalf("stored intervals %d, want 0 for pure-\\n text", m.Count())
	}
	if m.ToOrig(n/3) != n/3 || m.ToOut(2*n/3) != 2*n/3 {
		t.Fatal("identity map wrong")
	}
}
