package api_test

import (
	"errors"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

type naive map[string]api.Record

func must(m map[string]api.Record, _ error) map[string]api.Record { return m }
func (n naive) bump(e string, ts int64, add bool) {
	r := n[e]
	if add {
		r.A = max(r.A, ts)
	} else {
		r.R = max(r.R, ts)
	}
	n[e] = r
}
func (n naive) merge(o map[string]api.Record) {
	for e, r := range o {
		n.bump(e, r.A, true)
		n.bump(e, r.R, false)
	}
}
func apply(s *api.Set, r int, e string, ts int64, add bool) {
	if add {
		s.Add(r, e, ts)
	} else {
		s.Remove(r, e, ts)
	}
}
func checkAll(t *testing.T, s *api.Set, n int, ref naive) {
	if err := s.SyncAll(); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < n; r++ {
		if m, _ := s.Records(r); !maps.Equal(m, ref) {
			t.Fatalf("replica %d mismatch", r)
		}
	}
}
func TestNaiveReference(t *testing.T) {
	for _, tc := range [][3]int{{2, 200, 1}, {4, 500, 2}, {5, 800, 3}} {
		s, _ := api.New(tc[0], 100000)
		rng, ref := rand.New(rand.NewSource(int64(tc[2]))), naive{}
		for i := 0; i < tc[1]; i++ {
			r, e, ts := rng.Intn(tc[0]), string(rune('a'+rng.Intn(12))), int64(rng.Intn(20)+1)
			add := rng.Intn(2) == 0
			apply(s, r, e, ts, add)
			ref.bump(e, ts, add)
			if i%11 == 0 {
				s.Merge(rng.Intn(tc[0]), rng.Intn(tc[0]))
			}
		}
		checkAll(t, s, tc[0], ref)
	}
}
func TestIncrementalEquivalence(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		s, _ := api.New(3, 100000)
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 40; i++ {
			for j := 0; j < 5; j++ {
				r, e, ts := rng.Intn(3), string(rune('a'+rng.Intn(8))), int64(rng.Intn(15)+1)
				apply(s, r, e, ts, rng.Intn(2) == 0)
			}
			dst, src := rng.Intn(3), rng.Intn(3)
			bd, bs := must(s.Records(dst)), must(s.Records(src))
			s.Merge(dst, src)
			want := naive{}
			want.merge(bd)
			want.merge(bs)
			if got, _ := s.Records(dst); !maps.Equal(got, want) {
				t.Fatalf("seed %d step %d: incremental != full", seed, i)
			}
		}
	}
}
func TestFailureAtomicity(t *testing.T) {
	newErr := func(n, m int) error { _, e := api.New(n, m); return e }
	s, _ := api.New(2, 2)
	s.Add(0, "a", 1)
	s.Add(0, "b", 2)
	before, _ := s.Records(0)
	cases := [][2]error{
		{newErr(0, 1), api.ErrParam}, {newErr(1, 0), api.ErrParam}, {s.Merge(2, 0), api.ErrParam}, {s.Merge(0, -1), api.ErrParam},
		{s.Add(9, "x", 1), api.ErrParam}, {s.Remove(-1, "x", 1), api.ErrParam}, {s.Add(0, "", 1), api.ErrElement}, {s.Remove(0, "", 1), api.ErrElement},
		{s.Add(0, "c", 0), api.ErrTimestamp}, {s.Remove(0, "c", -3), api.ErrTimestamp}, {s.Add(0, "c", 3), api.ErrCapacity}, {s.Remove(0, "c", 4), api.ErrCapacity},
	}
	for i, c := range cases {
		if !errors.Is(c[0], c[1]) {
			t.Fatalf("case %d: got %v, want %v", i, c[0], c[1])
		}
	}
	if after, _ := s.Records(0); !maps.Equal(before, after) {
		t.Fatal("rejected ops changed records")
	}
	g, _ := api.New(3, 4) // rejected Merge leaves no trace; later merges stay complete
	for r, es := range [][]string{{"a", "b"}, {"c", "d", "e"}, {"c", "d"}} {
		for i, e := range es {
			g.Add(r, e, int64(i+1))
		}
	}
	snap0, _ := g.Records(0)
	if rej := g.Merge(0, 1); !errors.Is(rej, api.ErrCapacity) || !maps.Equal(snap0, must(g.Records(0))) || g.Merge(0, 2) != nil {
		t.Fatal("rejected merge left trace")
	}
	if el, _ := g.Elements(0); !slices.Equal(el, []string{"a", "b", "c", "d"}) {
		t.Fatal("post-rejection merge incomplete")
	}
}
func TestConcurrency(t *testing.T) {
	s, _ := api.New(4, 100000)
	parts := []naive{{}, {}, {}, {}}
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				if g >= 4 {
					s.Merge(rng.Intn(4), rng.Intn(4))
					continue
				}
				e, ts, add := string(rune('a'+rng.Intn(10))), int64(rng.Intn(30)+1), rng.Intn(2) == 0
				apply(s, g, e, ts, add)
				parts[g].bump(e, ts, add)
			}
		}(g)
	}
	wg.Wait()
	for _, p := range parts[1:] {
		parts[0].merge(p)
	}
	checkAll(t, s, 4, parts[0])
}
func TestSelfCheck(t *testing.T) {
	if s, err := api.New(2, 10); err != nil || s.SelfCheck() != nil {
		t.Fatalf("selfcheck failed: %v", err)
	}
}
