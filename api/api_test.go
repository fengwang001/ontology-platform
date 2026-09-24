package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"

	"ontology/api"
	"ontology/orset"
)

// Naive reference: recompute from the full op history — an element is present
// iff some successful Add's tag was never observed by a successful Remove.
func TestNaiveConsistency(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 4} {
		rng := rand.New(rand.NewPCG(seed, 9))
		s, _ := api.New(3, 1000000)
		var seq [3]int
		allA, allT := map[orset.Tag]string{}, map[orset.Tag]bool{}
		for i := 0; i < 400; i++ {
			r, e := rng.IntN(3), fmt.Sprintf("e%d", rng.IntN(8))
			switch rng.IntN(3) {
			case 0:
				if err := s.Add(r, e); err != nil {
					t.Fatal(err)
				}
				seq[r]++
				allA[orset.Tag{Replica: r, Seq: seq[r]}] = e
			case 1:
				before, _ := s.Elements(r)
				if err := s.Remove(r, e); err != nil && len(before[e]) > 0 {
					t.Fatalf("seed %d op %d: remove failed on live element", seed, i)
				}
				for _, tag := range before[e] {
					allT[tag] = true
				}
			case 2:
				if err := s.Merge(rng.IntN(3), r); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := s.SyncAll(); err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{}
		for tag, e := range allA {
			if !allT[tag] {
				want[e] = true
			}
		}
		for r := 0; r < 3; r++ {
			el, _ := s.Elements(r)
			if got := slices.Sorted(maps.Keys(el)); !slices.Equal(got, slices.Sorted(maps.Keys(want))) {
				t.Fatalf("seed %d replica %d: got %v want %v", seed, r, got, want)
			}
		}
	}
}

// A remove must not kill a concurrent add it never observed.
func TestSelfCheck(t *testing.T) {
	s, _ := api.New(2, 8)
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// A remove must not kill a concurrent add it never observed.
func TestAddWins(t *testing.T) {
	s, _ := api.New(2, 100)
	s.Add(0, "x") // tag {0,1}
	s.Merge(1, 0)
	s.Add(0, "x") // tag {0,2}, not observed by replica 1
	s.Remove(1, "x")
	s.Merge(1, 0)
	el, _ := s.Elements(1)
	if got := fmt.Sprint(el); got != "map[x:[{0 2}]]" {
		t.Fatalf("got %s, want map[x:[{0 2}]]", got)
	}
}

func TestErrorsDistinct(t *testing.T) {
	if _, err := api.New(0, 1); !errors.Is(err, orset.ErrInvalidArgument) {
		t.Fatal("bad n not rejected")
	}
	s, _ := api.New(2, 1)
	s.Add(0, "a")
	cases := [][2]error{
		{s.Add(5, "b"), orset.ErrInvalidArgument}, {s.Merge(7, 0), orset.ErrInvalidArgument},
		{s.Merge(0, 7), orset.ErrInvalidArgument}, {s.Add(0, ""), orset.ErrEmptyElement},
		{s.Remove(0, "zz"), orset.ErrNotFound}, {s.Add(0, "b"), orset.ErrCapacity},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		if !errors.Is(c[0], c[1]) {
			t.Fatalf("got %v, want %v", c[0], c[1])
		}
		seen[c[1]] = true
	}
	if len(seen) != 4 {
		t.Fatal("error kinds not distinct")
	}
}

// Concurrent adds/removes per replica plus random merges; afterwards every
// replica must equal the naive reference (odd elements survive).
func TestConcurrent(t *testing.T) {
	s, _ := api.New(4, 100000)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Go(func() {
			for j := 0; j < 25; j++ {
				s.Add(g, fmt.Sprintf("g%d-%d", g, j))
			}
			for j := 0; j < 25; j += 2 {
				s.Remove(g, fmt.Sprintf("g%d-%d", g, j))
			}
		})
	}
	for m := 0; m < 2; m++ {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(m), 1))
			for k := 0; k < 200; k++ {
				s.Merge(rng.IntN(4), rng.IntN(4))
			}
		})
	}
	wg.Wait()
	if err := s.SyncAll(); err != nil {
		t.Fatal(err)
	}
	var want []string
	for g := 0; g < 4; g++ {
		for j := 1; j < 25; j += 2 {
			want = append(want, fmt.Sprintf("g%d-%d", g, j))
		}
	}
	slices.Sort(want)
	for r := 0; r < 4; r++ {
		el, _ := s.Elements(r)
		if got := slices.Sorted(maps.Keys(el)); !slices.Equal(got, want) {
			t.Fatalf("replica %d: got %v", r, got)
		}
	}
}
