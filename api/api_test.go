package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
)

// gen builds a deterministic stream of n keys in [0, keySpace).
func gen(n, keySpace, seed int) []int {
	out := make([]int, n)
	s := uint64(seed*2654435761 + 1)
	for i := range out {
		s = s*6364136223846793005 + 1442695040888963407
		out[i] = int(s>>33) % keySpace
	}
	return out
}

func TestCanonicalReplay(t *testing.T) {
	c, _ := api.New(3)
	_ = c.Feed([]int{3, 1, 3, 2, 4, 1, 3, 5})
	want := []api.Entry{{Key: 3, Count: 3}, {Key: 5, Count: 3, Err: 2}, {Key: 4, Count: 2, Err: 1}}
	if got := c.TopK(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("TopK() = %v, want %v", got, want)
	}
	for x, q := range map[int]int{5: 3, 1: 0, 2: 0, 3: 3, 4: 2} {
		if got := c.Query(x); got != q {
			t.Fatalf("Query(%d) = %d, want %d", x, got, q)
		}
	}
}

func TestInvariantsVsNaive(t *testing.T) {
	cases := []struct{ k, n, keySpace, seed int }{
		{1, 500, 4, 1}, {2, 500, 5, 2}, {3, 8, 6, 3}, {3, 1000, 7, 4},
		{5, 2000, 20, 5}, {50, 5000, 200, 6}, {100, 5000, 1000, 7},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("k=%d/n=%d", tc.k, tc.n), func(t *testing.T) {
			c, _ := api.New(tc.k)
			tru := map[int]int{}
			for _, x := range gen(tc.n, tc.keySpace, tc.seed) {
				_ = c.Feed([]int{x})
				tru[x]++
				if len(c.TopK()) > tc.k {
					t.Fatalf("capacity %d exceeded", tc.k)
				}
			}
			entries := c.TopK()
			min := entries[len(entries)-1].Count
			mon := map[int]api.Entry{}
			for _, e := range entries {
				mon[e.Key] = e
			}
			for x, tc2 := range tru {
				if e, ok := mon[x]; ok {
					if q := c.Query(x); q < tc2 || e.Count-e.Err > tc2 {
						t.Fatalf("bound key %d: %d not in [%d,%d]", x, tc2, e.Count-e.Err, q)
					}
				} else if tc2 > min {
					t.Fatalf("min bound key %d: %d > %d", x, tc2, min)
				}
			}
		})
	}
}

func TestRejectAtomic(t *testing.T) {
	for _, k := range []int{0, -3} {
		if _, err := api.New(k); !errors.Is(err, api.ErrBadK) {
			t.Fatalf("New(%d) err = %v", k, err)
		}
	}
	c, _ := api.New(3)
	if err := c.Feed([]int{1, 2, 2}); err != nil {
		t.Fatal(err)
	}
	before := fmt.Sprint(c.TopK())
	rejects := []struct {
		name string
		feed []int
		want error
	}{
		{"nil", nil, api.ErrNilFeed},
		{"negative", []int{4, -1, 5}, api.ErrNegativeKey},
	}
	for _, r := range rejects {
		if err := c.Feed(r.feed); !errors.Is(err, r.want) {
			t.Fatalf("%s: err = %v, want %v", r.name, err, r.want)
		}
		if got := fmt.Sprint(c.TopK()); got != before {
			t.Fatalf("%s: state changed to %v", r.name, got)
		}
	}
	for _, p := range [][2]error{{api.ErrBadK, api.ErrNilFeed}, {api.ErrBadK, api.ErrNegativeKey}, {api.ErrNilFeed, api.ErrNegativeKey}} {
		if errors.Is(p[0], p[1]) {
			t.Fatalf("sentinels not distinct: %v vs %v", p[0], p[1])
		}
	}
	if err := c.Feed([]int{7}); err != nil || c.Query(7) != 1 {
		t.Fatalf("instance unusable after rejects: err=%v q=%d", err, c.Query(7))
	}
}

func TestConcurrentReadsConsistent(t *testing.T) {
	c, _ := api.New(10)
	_ = c.Feed(gen(2000, 30, 9))
	wantTop := fmt.Sprint(c.TopK())
	wantQ := map[int]int{}
	for x := 0; x < 30; x++ {
		wantQ[x] = c.Query(x)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				if fmt.Sprint(c.TopK()) != wantTop {
					t.Error("TopK mismatch")
					return
				}
				for x, q := range wantQ {
					if c.Query(x) != q {
						t.Errorf("Query(%d) mismatch", x)
						return
					}
				}
			}
		}()
	}
	close(start)
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	c, _ := api.New(5)
	if err := c.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
