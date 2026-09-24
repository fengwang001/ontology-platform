package search

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/vec"
)

const (
	testDims = 32
	testN    = 5000
	testQ    = 100
)

// genClustered builds n points around 50 random centers (small noise),
// giving nearest-neighbor queries a meaningful, reproducible structure.
func genClustered(n int, seed int64) []vec.Vec {
	r := rand.New(rand.NewSource(seed))
	centers := make([]vec.Vec, 50)
	for i := range centers {
		c := make(vec.Vec, testDims)
		for j := range c {
			c[j] = r.NormFloat64()
		}
		centers[i] = c
	}
	out := make([]vec.Vec, n)
	for i := range out {
		v := make(vec.Vec, testDims)
		c := centers[r.Intn(len(centers))]
		for j := range v {
			v[j] = c[j] + 0.15*r.NormFloat64()
		}
		out[i] = v
	}
	return out
}

func buildIndex(vecs []vec.Vec, bits, tables int) *Index {
	ix := New(testDims, bits, tables, 1000)
	for _, v := range vecs {
		if err := ix.Add(v); err != nil {
			panic(err)
		}
	}
	return ix
}

func evalIndex(ix *Index, queries []vec.Vec, exact [][]int) (recall, cands float64) {
	ix.ResetStats()
	for i, q := range queries {
		approx, err := ix.Query(q, 10)
		if err != nil {
			panic(err)
		}
		set := make(map[int]bool, 10)
		for _, id := range exact[i] {
			set[id] = true
		}
		hit := 0
		for _, id := range approx {
			if set[id] {
				hit++
			}
		}
		recall += float64(hit) / 10
	}
	return recall / float64(len(queries)), float64(ix.DistCount()) / float64(len(queries))
}

func TestRecallMatrix(t *testing.T) {
	all := genClustered(testN+testQ, 7)
	vecs, queries := all[:testN], all[testN:]
	exact := make([][]int, len(queries))
	for i, q := range queries {
		exact[i] = BruteForce(vecs, q, 10)
	}
	bs := []int{4, 8, 12}
	ls := []int{1, 2, 4, 8}
	cand := map[[2]int]float64{}
	for _, b := range bs {
		prev := -1.0
		for _, l := range ls {
			r, c := evalIndex(buildIndex(vecs, b, l), queries, exact)
			cand[[2]int{b, l}] = c
			t.Logf("b=%2d L=%d candidates=%7.1f recall=%.3f", b, l, c, r)
			if r < prev {
				t.Errorf("b=%d: recall non-monotonic in L", b)
			}
			prev = r
			if b == 8 && l == 8 && r < 0.6 {
				t.Errorf("b=8 L=8: recall=%.3f < 0.6", r)
			}
		}
	}
	for _, l := range ls {
		if !(cand[[2]int{4, l}] > cand[[2]int{8, l}] && cand[[2]int{8, l}] > cand[[2]int{12, l}]) {
			t.Errorf("L=%d: candidates not strictly decreasing in b", l)
		}
	}
}

func TestCounters(t *testing.T) {
	vecs := genClustered(testN, 7)
	ix := buildIndex(vecs, 8, 8)
	if got := ix.HashCount(); got != uint64(testN*8*8) {
		t.Errorf("HashCount=%d want %d", got, testN*8*8)
	}
	ix.ResetStats()
	for _, q := range vecs[:100] {
		if _, err := ix.Query(q, 10); err != nil {
			t.Fatal(err)
		}
	}
	if got := ix.DistCount(); got > uint64(100*testN/10) {
		t.Errorf("DistCount=%d exceeds 10%% of N per query", got)
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"empty index", func(t *testing.T) {
			ix := New(4, 4, 2, 1)
			got, err := ix.Query(vec.Vec{1, 2, 3, 4}, 10)
			if err != nil || len(got) != 0 {
				t.Errorf("got %v, %v", got, err)
			}
		}},
		{"single vector, k>n", func(t *testing.T) {
			ix := New(3, 4, 2, 1)
			if err := ix.Add(vec.Vec{1, 2, 3}); err != nil {
				t.Fatal(err)
			}
			got, err := ix.Query(vec.Vec{1, 2, 3}, 10)
			if err != nil || len(got) != 1 || got[0] != 0 {
				t.Errorf("got %v, %v", got, err)
			}
		}},
		{"identical vectors recall 1", func(t *testing.T) {
			ix := New(3, 8, 4, 5)
			for i := 0; i < 100; i++ {
				if err := ix.Add(vec.Vec{1, 2, 3}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ix.Query(vec.Vec{1, 2, 3}, 10)
			if err != nil || len(got) != 10 {
				t.Errorf("got %d ids, %v", len(got), err)
			}
		}},
		{"NaN and Inf rejected", func(t *testing.T) {
			ix := New(3, 4, 2, 1)
			bad := []vec.Vec{{math.NaN(), 1, 2}, {math.Inf(1), 1, 2}, {math.Inf(-1), 1, 2}}
			for _, v := range bad {
				if err := ix.Add(v); !errors.Is(err, ErrNonFinite) {
					t.Errorf("err=%v want ErrNonFinite", err)
				}
			}
			if ix.Skipped() != 3 || ix.Len() != 0 {
				t.Errorf("skipped=%d len=%d", ix.Skipped(), ix.Len())
			}
		}},
		{"zero vector legal", func(t *testing.T) {
			ix := New(3, 4, 2, 1)
			if err := ix.Add(vec.Vec{0, 0, 0}); err != nil {
				t.Fatal(err)
			}
			got, err := ix.Query(vec.Vec{0, 0, 0}, 1)
			if err != nil || len(got) != 1 {
				t.Errorf("got %v, %v", got, err)
			}
		}},
		{"dimension 1", func(t *testing.T) {
			ix := New(1, 4, 2, 1)
			for _, x := range []float64{-2, 0.5, 3} {
				if err := ix.Add(vec.Vec{x}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := ix.Query(vec.Vec{0.4}, 1)
			if err != nil || len(got) != 1 || got[0] != 1 {
				t.Errorf("got %v, %v", got, err)
			}
		}},
		{"dimension mismatch", func(t *testing.T) {
			ix := New(4, 4, 2, 1)
			if err := ix.Add(vec.Vec{1, 2, 3}); !errors.Is(err, vec.ErrDim) {
				t.Errorf("add err=%v want ErrDim", err)
			}
			if _, err := ix.Query(vec.Vec{1, 2, 3}, 1); !errors.Is(err, vec.ErrDim) {
				t.Errorf("query err=%v want ErrDim", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestConcurrentBuildAndQuery(t *testing.T) {
	ix := New(8, 4, 2, 9)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(2)
		go func(base float64) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				_ = ix.Add(vec.Vec{base, float64(i), 1, 2, 3, 4, 5, 6})
			}
		}(float64(g))
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				got, err := ix.Query(vec.Vec{1, 2, 3, 4, 5, 6, 7, 8}, 5)
				if err != nil {
					t.Error(err)
					return
				}
				for _, id := range got {
					if id < 0 || id >= ix.Len() {
						t.Errorf("dangling id %d (len %d)", id, ix.Len())
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	if ix.Len() != 1000 {
		t.Errorf("len=%d want 1000", ix.Len())
	}
}
