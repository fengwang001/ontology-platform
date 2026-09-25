package api_test

import (
	"errors"
	"testing"

	"ontology/api"
)

// TestInvariantConservation pins invariant 1 through the public API: placed
// objects stay inside one arena and their intervals never overlap.
func TestInvariantConservation(t *testing.T) {
	cases := []struct {
		size, al, max int
		ns            []int
	}{
		{16, 8, 3, []int{5, 10, 5, 3}},
		{32, 4, 4, []int{1, 7, 8, 33, 2}},
		{64, 16, 3, []int{16, 16, 64, 64, 3}},
	}
	for _, c := range cases {
		a, err := api.New(c.size, c.al, c.max)
		if err != nil {
			t.Fatal(err)
		}
		type iv struct{ off, sz int }
		var got []iv
		for _, n := range c.ns {
			off, _, e := a.Alloc(n)
			if e != nil {
				continue
			}
			sz := (n + c.al - 1) &^ (c.al - 1)
			if off/c.size != (off+sz-1)/c.size || off+sz > c.size*c.max {
				t.Fatalf("(%d,+%d) straddles/escapes an arena", off, sz)
			}
			got = append(got, iv{off, sz})
		}
		for i := range got {
			for j := 0; j < i; j++ {
				p, q := got[i], got[j]
				if p.off < q.off+q.sz && q.off < p.off+p.sz {
					t.Fatalf("overlap %+v %+v", p, q)
				}
			}
		}
	}
}

// TestInvariantAlignmentDangling pins invariant 3 via Valid: every offset is a
// multiple of align; old handles die on Reset, new ones stay valid.
func TestInvariantAlignmentDangling(t *testing.T) {
	cases := []struct{ size, al, max int }{
		{16, 8, 3}, {40, 4, 5}, {64, 1, 2},
	}
	for _, c := range cases {
		a, _ := api.New(c.size, c.al, c.max)
		type h struct{ off, gen int }
		var old []h
		for i := 0; i < 6; i++ {
			off, gen, e := a.Alloc(1 + i)
			if e != nil {
				t.Fatal(e)
			}
			if off%c.al != 0 || !a.Valid(off, gen) {
				t.Fatalf("bad fresh handle (%d,%d)", off, gen)
			}
			old = append(old, h{off, gen})
		}
		a.Reset()
		for _, p := range old {
			if a.Valid(p.off, p.gen) {
				t.Fatalf("pre-Reset handle (%d,%d) survived", p.off, p.gen)
			}
		}
		off, gen, e := a.Alloc(3)
		if e != nil || off%c.al != 0 || !a.Valid(off, gen) {
			t.Fatalf("post-Reset alloc bad: (%d,%d,%v)", off, gen, e)
		}
	}
}

// TestFourSentinelErrors pins section 5: four failure modes are pairwise
// distinct and decidable, and rejections leave the allocator usable.
func TestFourSentinelErrors(t *testing.T) {
	_, eAlign := api.New(8, 6, 1)
	_, eSmall := api.New(4, 8, 1)
	_, eMax := api.New(16, 8, 0)
	a, _ := api.New(16, 8, 1)
	a.Alloc(8)
	_, _, eSize := a.Alloc(0)
	_, _, eExh := a.Alloc(16)
	got := []error{eSize, eAlign, eSmall, eExh}
	want := []error{
		api.ErrInvalidSize, api.ErrInvalidAlign,
		api.ErrArenaTooSmall, api.ErrArenaExhausted,
	}
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			t.Fatalf("case %d: got %v want %v", i, got[i], want[i])
		}
		for j := range got {
			if j != i && errors.Is(got[i], want[j]) {
				t.Fatalf("case %d unexpectedly matches sentinel %d", i, j)
			}
		}
	}
	if !errors.Is(eMax, api.ErrArenaExhausted) {
		t.Fatalf("maxArenas<1: got %v want ErrArenaExhausted", eMax)
	}
	// Rejections left bump=8; arena0's 8-byte tail still serves Alloc(8).
	if off, _, e := a.Alloc(8); e != nil || off != 8 {
		t.Fatalf("unusable after rejection: off=%d e=%v", off, e)
	}
}

// TestSelfCheck pins all four invariants on the built-in section-3 script and
// the random interleavings, callable directly by tests (section 2).
func TestSelfCheck(t *testing.T) {
	a, err := api.New(16, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestCanonicalEightSteps pins the NOTES section-3 eight-step table exactly.
func TestCanonicalEightSteps(t *testing.T) {
	a, _ := api.New(16, 8, 3)
	want := []struct{ off, gen int }{
		{0, 0}, {16, 0}, {32, 0}, {40, 0}, {0, 1}, {8, 1},
	}
	for i, n := range []int{5, 10, 5, 3} {
		off, gen, e := a.Alloc(n)
		if e != nil || off != want[i].off || gen != want[i].gen {
			t.Fatalf("step %d: (%d,%d,%v) want %+v", i+1, off, gen, e, want[i])
		}
	}
	a.Reset()
	for i, n := range []int{5, 5} {
		if off, gen, e := a.Alloc(n); e != nil || off != want[4+i].off || gen != 1 {
			t.Fatalf("step %d: (%d,%d,%v)", 6+i, off, gen, e)
		}
	}
	if a.Valid(0, 0) || !a.Valid(0, 1) {
		t.Fatal("Valid(0,0)=false and Valid(0,1)=true required after Reset")
	}
}
