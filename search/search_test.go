package search

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/bucket"
	"ontology/vec"
)

func genClustered(n, dim, clusters, nq int, seed int64) ([]vec.Vector, []vec.Vector) {
	rng := rand.New(rand.NewSource(seed))
	centers := make([]vec.Vector, clusters)
	for i := range centers {
		c := make(vec.Vector, dim)
		for j := range c {
			c[j] = 4 * rng.NormFloat64()
		}
		centers[i] = c
	}
	point := func() vec.Vector {
		c := centers[rng.Intn(clusters)]
		v := make(vec.Vector, dim)
		for j := range v {
			v[j] = c[j] + rng.NormFloat64()
		}
		return v
	}
	gen := func(m int) []vec.Vector {
		out := make([]vec.Vector, m)
		for i := range out {
			out[i] = point()
		}
		return out
	}
	return gen(n), gen(nq)
}

func build(vecs []vec.Vector, dim, bits, tables int) *Index {
	ix := New(dim, bits, tables, 42)
	for _, v := range vecs {
		if err := ix.Add(v); err != nil {
			panic(err)
		}
	}
	return ix
}

func recallCand(ix *Index, exact [][]int, qs []vec.Vector, k int) (float64, float64) {
	ix.ResetDistOps()
	sum := 0.0
	for i, q := range qs {
		got, _ := ix.Query(q, k)
		set := make(map[int]bool, len(got))
		for _, id := range got {
			set[id] = true
		}
		hit := 0
		for _, id := range exact[i] {
			if set[id] {
				hit++
			}
		}
		sum += float64(hit) / float64(k)
	}
	return sum / float64(len(qs)), float64(ix.DistOps()) / float64(len(qs))
}

func TestRecallCandidateMatrix(t *testing.T) {
	const dim, n, nq, k = 32, 5000, 100, 10
	vecs, qs := genClustered(n, dim, 100, nq, 7)
	exact := make([][]int, nq)
	for i, q := range qs {
		exact[i] = BruteForce(vecs, q, k)
	}
	bs, ls := []int{4, 8, 12}, []int{1, 2, 4, 8}
	rec := map[[2]int]float64{}
	cand := map[[2]int]float64{}
	for _, b := range bs {
		for _, l := range ls {
			ix := build(vecs, dim, b, l)
			r, c := recallCand(ix, exact, qs, k)
			rec[[2]int{b, l}], cand[[2]int{b, l}] = r, c
			t.Logf("b=%2d L=%d candidates=%7.1f recall@10=%.3f", b, l, c, r)
		}
	}
	if r := rec[[2]int{8, 8}]; r < 0.6 {
		t.Errorf("recall@10 (b=8,L=8) = %.3f, want >= 0.6", r)
	}
	if c := cand[[2]int{8, 8}]; c > 500 {
		t.Errorf("rerank dist ops (b=8,L=8) = %.1f, exceeds 10%% of 5000", c)
	}
	for i := 1; i < len(ls); i++ { // recall non-decreasing in L at b=8
		if rec[[2]int{8, ls[i]}] < rec[[2]int{8, ls[i-1]}] {
			t.Errorf("recall not monotone at L=%d: %.3f < %.3f",
				ls[i], rec[[2]int{8, ls[i]}], rec[[2]int{8, ls[i-1]}])
		}
	}
	for i := 1; i < len(bs); i++ { // candidates decrease in b at L=4
		if cand[[2]int{bs[i], 4}] >= cand[[2]int{bs[i-1], 4}] {
			t.Errorf("candidates not decreasing: b=%d %.1f >= b=%d %.1f",
				bs[i], cand[[2]int{bs[i], 4}], bs[i-1], cand[[2]int{bs[i-1], 4}])
		}
	}
}

func TestEdgeCases(t *testing.T) {
	empty := New(4, 4, 2, 1)
	if got, _ := empty.Query(vec.Vector{1, 2, 3, 4}, 10); len(got) != 0 {
		t.Errorf("empty index returned %d results", len(got))
	}
	one := build([]vec.Vector{{1, 2, 3, 4}}, 4, 4, 2)
	if got, _ := one.Query(vec.Vector{1, 2, 3, 4}, 10); len(got) != 1 {
		t.Errorf("k>n: got %d results, want 1", len(got))
	}
	if _, err := one.Query(vec.Vector{1, 2, 3}, 1); !errors.Is(err, vec.ErrDimMismatch) {
		t.Errorf("dim mismatch: err = %v", err)
	}
	bad := []struct {
		v    vec.Vector
		want error
	}{
		{vec.Vector{1, math.NaN(), 3, 4}, vec.ErrNaN},
		{vec.Vector{1, math.Inf(1), 3, 4}, vec.ErrInf},
		{vec.Vector{1, math.Inf(-1), 3, 4}, vec.ErrInf},
	}
	for i, c := range bad {
		if err := one.Add(c.v); !errors.Is(err, c.want) {
			t.Errorf("bad[%d]: err = %v, want %v", i, err, c.want)
		}
	}
	if one.Skipped() != 3 {
		t.Errorf("Skipped = %d, want 3", one.Skipped())
	}
	if err := one.Add(vec.Vector{0, 0, 0, 0}); err != nil {
		t.Errorf("zero vector rejected: %v", err)
	}
	d1 := build([]vec.Vector{{1}, {2}, {3}}, 1, 4, 2)
	if got, _ := d1.Query(vec.Vector{1.1}, 1); len(got) != 1 || got[0] != 0 {
		t.Errorf("dim=1 query: got %v, want [0]", got)
	}
	sameV := make([]vec.Vector, 300)
	for i := range sameV {
		sameV[i] = vec.Vector{1, 2, 3, 4}
	}
	ixSame := build(sameV, 4, 8, 8)
	ixSame.ResetDistOps()
	got, _ := ixSame.Query(vec.Vector{1, 2, 3, 4}, 10)
	if ixSame.DistOps() != 300 || len(got) != 10 {
		t.Errorf("identical vectors: candidates=%d want 300, top10=%d", ixSame.DistOps(), len(got))
	}
}

func TestConcurrentBuildQuery(t *testing.T) {
	bk := bucket.New(2) // bucket: dedup union across tables
	bk.Add(1, []uint64{5, 5})
	bk.Add(2, []uint64{5, 7})
	if got := bk.Candidates([]uint64{5, 5}); len(got) != 2 {
		t.Errorf("candidates = %v, want 2 unique ids (dedup across tables)", got)
	}
	if got := bk.Candidates([]uint64{9, 9}); len(got) != 0 {
		t.Errorf("miss candidates = %v, want empty", got)
	}
	vecs, qs := genClustered(2000, 8, 20, 20, 3)
	ix := New(8, 6, 4, 11)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // builder
		defer wg.Done()
		for _, v := range vecs {
			_ = ix.Add(v)
		}
	}()
	for w := 0; w < 4; w++ { // concurrent readers
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				got, err := ix.Query(qs[i%len(qs)], 5)
				bad := err != nil
				for _, id := range got {
					bad = bad || id < 0 || id >= ix.Len()
				}
				if bad {
					t.Errorf("iter %d: err=%v got=%v len=%d", i, err, got, ix.Len())
					return
				}
			}
		}()
	}
	wg.Wait()
	if ix.Len() != 2000 {
		t.Errorf("Len = %d, want 2000", ix.Len())
	}
}
