package blk

import (
	"math/rand"
	"testing"
)

func TestPartsCover(t *testing.T) {
	cases := []struct{ n, b int }{
		{1, 1}, {2, 1}, {5, 2}, {6, 3}, {7, 4}, {10, 3}, {17, 5}, {100, 17},
	}
	r := rand.New(rand.NewSource(1))
	r.Shuffle(len(cases), func(i, j int) { cases[i], cases[j] = cases[j], cases[i] })
	for _, c := range cases {
		ps := Parts(c.n, c.b)
		if len(ps) != (c.n+c.b-1)/c.b {
			t.Fatalf("Parts(%d,%d): got %d blocks", c.n, c.b, len(ps))
		}
		seen := make([]int, c.n) // each index belongs to exactly one block
		for qi, p := range ps {
			if p.Start < 0 || p.End > c.n || p.End <= p.Start {
				t.Fatalf("Parts(%d,%d) bad block %d: %+v", c.n, c.b, qi, p)
			}
			if qi > 0 && p.Start != ps[qi-1].End {
				t.Fatalf("Parts(%d,%d): gap/overlap at block %d", c.n, c.b, qi)
			}
			for x := p.Start; x < p.End; x++ {
				seen[x]++
			}
			want := c.b
			if c.n%c.b != 0 && qi == len(ps)-1 {
				want = c.n % c.b
			}
			if p.End-p.Start != want {
				t.Fatalf("Parts(%d,%d) block %d size=%d want %d", c.n, c.b, qi, p.End-p.Start, want)
			}
		}
		if ps[0].Start != 0 || ps[len(ps)-1].End != c.n {
			t.Fatalf("Parts(%d,%d) does not span [0,%d)", c.n, c.b, c.n)
		}
		for x, n := range seen {
			if n != 1 {
				t.Fatalf("Parts(%d,%d): index %d covered %d times", c.n, c.b, x, n)
			}
		}
		if got := Parts(0, 1); got != nil {
			t.Fatalf("Parts(0,1) = %v, want nil", got)
		}
		if got := Parts(5, 0); got != nil {
			t.Fatalf("Parts(5,0) = %v, want nil", got)
		}
		if got := Parts(5, 6); got != nil {
			t.Fatalf("Parts(5,6) = %v, want nil", got)
		}
	}
}

func TestTailBlockValues(t *testing.T) {
	want := map[int][2]int{0: {0, 2}, 1: {0, 2}, 2: {2, 2}, 3: {2, 2}, 4: {4, 1}}
	for k, w := range want {
		s, sz := TailBlock(5, 2, k)
		if s != w[0] || sz != w[1] {
			t.Fatalf("TailBlock(5,2,%d)=(%d,%d) want %v", k, s, sz, w)
		}
	}
	for _, args := range [][3]int{{0, 1, 0}, {5, 0, 0}, {5, 6, 0}, {5, 2, -1}, {5, 2, 5}} {
		if s, sz := TailBlock(args[0], args[1], args[2]); s != 0 || sz != 0 {
			t.Fatalf("TailBlock%v=(%d,%d) want (0,0)", args, s, sz)
		}
	}
}

// TestTailBlockProbeConstant proves tail localization is O(1) closed
// form: for n=100/1000/10000 (b=17) the probe must stay exactly zero.
func TestTailBlockProbeConstant(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		for dim := 0; dim < 3; dim++ { // locate the tail block on i, j and k axes
			k := n - 1 // index guaranteed to lie in the tail block
			s, sz := TailBlock(n, 17, k)
			if sz != n%17 || s != n-n%17 {
				t.Fatalf("n=%d dim=%d: got start=%d size=%d want %d,%d",
					n, dim, s, sz, n-n%17, n%17)
			}
			if got := probeValue(); got != 0 {
				t.Fatalf("n=%d dim=%d: probe=%d, want 0 (linear scan?)", n, dim, got)
			}
		}
	}
}
