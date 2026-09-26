package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/wg"
)

type edge struct{ u, v, w int }
type graphCase struct {
	n  int
	es []edge
}

func build(t *testing.T, n int, es []edge) *api.Graph {
	g, err := api.New(n)
	for _, e := range es {
		err = errors.Join(err, g.AddEdge(e.u, e.v, int64(e.w)))
	}
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// enumMin enumerates bipartitions (node 0 fixed in S), min over accepted masks.
func enumMin(n int, es []edge, ok func(int) bool) int64 {
	inS := func(v, mask int) bool { return v == 0 || mask>>(v-1)&1 == 1 }
	best := int64(-1)
	for mask := 0; mask < 1<<(n-1); mask++ {
		var c int64
		for _, e := range es {
			if inS(e.u, mask) != inS(e.v, mask) {
				c += int64(e.w)
			}
		}
		if ok(mask) && (best < 0 || c < best) {
			best = c
		}
	}
	return best
}

func bruteForce(n int, es []edge) int64 { // ground truth: all proper bipartitions
	return enumMin(n, es, func(m int) bool { return m < 1<<(n-1)-1 })
}

func stReference(n int, es []edge) int64 { // naive reference: min over t of min 0-t cut
	best := int64(-1)
	for tgt := 1; tgt < n; tgt++ {
		if c := enumMin(n, es, func(m int) bool { return m>>(tgt-1)&1 == 0 }); best < 0 || c < best {
			best = c
		}
	}
	return best
}

func cases() []graphCase { // fixed + random graphs, random edge sets/weights/order
	cs := []graphCase{{4, []edge{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}}},
		{4, []edge{{0, 1, 10}, {2, 3, 10}, {0, 2, 1}, {1, 2, 1}, {0, 3, 1}, {1, 3, 1}}},
	}
	rng := rand.New(rand.NewSource(1))
	for n := 2; n <= 8; n++ {
		var pairs []edge
		for u := 0; u < n; u++ {
			for v := u + 1; v < n; v++ {
				pairs = append(pairs, edge{u: u, v: v})
			}
		}
		for rep := 0; rep < 10; rep++ {
			rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
			es := append([]edge(nil), pairs[:rng.Intn(len(pairs)+1)]...)
			for i := range es {
				es[i].w = 1 + rng.Intn(20)
			}
			cs = append(cs, graphCase{n, es})
		}
	}
	return cs
}

func checkAgainst(t *testing.T, ref func(int, []edge) int64) {
	for _, c := range cases() {
		got, err := build(t, c.n, c.es).MinCut()
		if want := ref(c.n, c.es); err != nil || got != want {
			t.Fatalf("n=%d es=%v: got %d err=%v, want %d", c.n, c.es, got, err, want)
		}
	}
}

func TestInvariantLowerBound(t *testing.T)  { checkAgainst(t, bruteForce) }
func TestInvariantSTReference(t *testing.T) { checkAgainst(t, stReference) }

func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	sents := []error{wg.ErrTooFewNodes, wg.ErrNodeOutOfRange, wg.ErrSelfLoop, wg.ErrDuplicateEdge}
	for i, a := range sents {
		for j, b := range sents {
			if (i == j) != errors.Is(a, b) {
				t.Fatalf("sentinels %d/%d not pairwise distinct", i, j)
			}
		}
	}
	if _, err := api.New(1); !errors.Is(err, wg.ErrTooFewNodes) {
		t.Fatalf("New(1) err = %v", err)
	}
	rejects := []struct {
		u, v int
		w    int64
		want error
	}{{-1, 1, 1, wg.ErrNodeOutOfRange}, {0, 3, 1, wg.ErrNodeOutOfRange},
		{2, 2, 1, wg.ErrSelfLoop}, {1, 0, 9, wg.ErrDuplicateEdge}}
	for _, r := range rejects {
		g := build(t, 3, []edge{{0, 1, 5}})
		if err := g.AddEdge(r.u, r.v, r.w); !errors.Is(err, r.want) {
			t.Fatalf("err = %v, want %v", err, r.want)
		}
		if g.EdgeCount() != 1 || g.AddEdge(1, 2, 4) != nil {
			t.Fatal("state changed or graph unusable after reject")
		}
	}
}

func TestConcurrentMinCutConsistent(t *testing.T) {
	g := build(t, 5, []edge{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}, {3, 4, 4}, {1, 4, 6}})
	want, _ := g.MinCut()
	var wg2 sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < 32; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for j := 0; j < 20; j++ {
				if mc, _ := g.MinCut(); mc != want {
					bad.Add(1)
				}
				_ = g.EdgeCount()
				_ = g.SelfCheck()
			}
		}()
	}
	wg2.Wait()
	if bad.Load() != 0 {
		t.Fatal("concurrent MinCut results differ")
	}
}
