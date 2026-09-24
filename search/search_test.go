package search

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/bucket"
	"ontology/vec"
)

const (
	testDim  = 16
	testN    = 5000
	testNQ   = 100
	testK    = 10
	testSeed = 7
	dataSeed = 99
	nCenters = 25
)

func synth(rng *rand.Rand) ([]vec.Vec, []vec.Vec) {
	centers := make([][]float64, nCenters)
	for i := range centers {
		c := make([]float64, testDim)
		for j := range c {
			c[j] = rng.NormFloat64() * 0.3
		}
		centers[i] = c
	}
	pick := func() vec.Vec {
		c := centers[rng.Intn(nCenters)]
		x := make(vec.Vec, testDim)
		for j := range x {
			x[j] = c[j] + rng.NormFloat64()*0.25
		}
		return x
	}
	vs := make([]vec.Vec, testN)
	for i := range vs {
		vs[i] = pick()
	}
	qs := make([]vec.Vec, testNQ)
	for i := range qs {
		qs[i] = pick()
	}
	return vs, qs
}

type cell struct {
	candidates float64
	recall     float64
}

func evalMatrix(t *testing.T, vs, qs []vec.Vec) map[string]cell {
	out := map[string]cell{}
	for _, b := range []int{4, 8, 12} {
		for _, L := range []int{1, 2, 4, 8} {
			idx, st, err := Build(testDim, b, L, testSeed, vs)
			if err != nil {
				t.Fatal(err)
			}
			if want := int64(testN * L * b); st.HashCalls != want {
				t.Fatalf("b=%d L=%d hash=%d want %d", b, L, st.HashCalls, want)
			}
			var cand, rec float64
			for _, q := range qs {
				ann, nc, err := idx.Query(q, testK)
				if err != nil {
					t.Fatal(err)
				}
				ex, err := idx.BruteForce(q, testK)
				if err != nil {
					t.Fatal(err)
				}
				hit := map[bucket.ID]bool{}
				for _, r := range ann {
					hit[r.ID] = true
				}
				var inter int
				for _, r := range ex {
					if hit[r.ID] {
						inter++
					}
				}
				cand += float64(nc)
				rec += float64(inter) / float64(testK)
			}
			key := fmt.Sprintf("%d/%d", b, L)
			out[key] = cell{cand / testNQ, rec / testNQ}
			t.Logf("b=%d L=%d candidates=%.1f recall=%.3f", b, L, cand/testNQ, rec/testNQ)
		}
	}
	return out
}

func TestRecallMatrix(t *testing.T) {
	rng := rand.New(rand.NewSource(dataSeed))
	vs, qs := synth(rng)
	m := evalMatrix(t, vs, qs)
	// Recall >= 0.6 at b=8,L=8 and monotone non-decreasing in L.
	for _, b := range []int{4, 8, 12} {
		prev := -1.0
		for _, L := range []int{1, 2, 4, 8} {
			c := m[fmt.Sprintf("%d/%d", b, L)]
			if c.recall < prev-1e-9 {
				t.Fatalf("recall decreased b=%d L=%d: %.3f < %.3f", b, L, c.recall, prev)
			}
			prev = c.recall
		}
	}
	if m["8/8"].recall < 0.6 {
		t.Fatalf("b=8 L=8 recall %.3f < 0.6", m["8/8"].recall)
	}
	// Candidates monotone decreasing in b (average over the four L values).
	for _, L := range []int{1, 2, 4, 8} {
		c4 := m[fmt.Sprintf("%d/%d", 4, L)].candidates
		c8 := m[fmt.Sprintf("%d/%d", 8, L)].candidates
		c12 := m[fmt.Sprintf("%d/%d", 12, L)].candidates
		if !(c4 > c8 && c8 > c12) {
			t.Fatalf("L=%d candidates not decreasing: %.1f %.1f %.1f", L, c4, c8, c12)
		}
	}
	// Rerank distance count at b=8,L=8 per query stays under 10% of N.
	idx, _, err := Build(testDim, 8, 8, testSeed, vs)
	if err != nil {
		t.Fatal(err)
	}
	idx.ResetDistances()
	for _, q := range qs {
		before := idx.ResetDistances()
		if _, err := idx.BruteForce(q, 0); err != nil {
			t.Fatal(err)
		}
		idx.ResetDistances()
		if _, _, err := idx.Query(q, testK); err != nil {
			t.Fatal(err)
		}
		dq := idx.ResetDistances()
		_ = before
		if dq > 500 {
			t.Fatalf("query rerank distances=%d > 500", dq)
		}
	}
}

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"empty", func(t *testing.T) {
			idx, _, err := Build(3, 4, 2, 1, nil)
			if err != nil {
				t.Fatal(err)
			}
			rs, nc, err := idx.Query(vec.Vec{0, 0, 0}, 5)
			if err != nil || len(rs) != 0 || nc != 0 {
				t.Fatalf("empty query rs=%v nc=%d err=%v", rs, nc, err)
			}
		}},
		{"single-and-k-too-large", func(t *testing.T) {
			idx, _, _ := Build(3, 4, 2, 1, []vec.Vec{{1, 2, 3}})
			rs, _, err := idx.Query(vec.Vec{1, 2, 3}, 10)
			if err != nil || len(rs) != 1 || rs[0].ID != 0 || rs[0].Dist != 0 {
				t.Fatalf("rs=%v err=%v", rs, err)
			}
		}},
		{"all-identical-recall-1", func(t *testing.T) {
			vs := make([]vec.Vec, 40)
			for i := range vs {
				vs[i] = vec.Vec{1, -1, 2}
			}
			idx, _, _ := Build(3, 6, 2, 5, vs)
			q := vec.Vec{1, -1, 2}
			ann, nc, _ := idx.Query(q, 10)
			ex, _ := idx.BruteForce(q, 10)
			if nc != 40 || len(ann) != 10 || len(ex) != 10 {
				t.Fatalf("nc=%d ann=%d ex=%d", nc, len(ann), len(ex))
			}
			hit := map[bucket.ID]bool{}
			for _, r := range ann {
				hit[r.ID] = true
			}
			for _, r := range ex {
				if !hit[r.ID] {
					t.Fatal("identical-data recall must be 1")
				}
			}
		}},
		{"nan-inf-skipped", func(t *testing.T) {
			vs := []vec.Vec{{1, 1}, {1, math.NaN()}, {math.Inf(1), 1}, {2, 2}}
			idx, st, err := Build(2, 4, 2, 3, vs)
			if err != nil || st.Vectors != 2 || st.Skipped != 2 {
				t.Fatalf("st=%+v err=%v", st, err)
			}
			if idx.Len() != 2 {
				t.Fatalf("len=%d", idx.Len())
			}
		}},
		{"dim-one", func(t *testing.T) {
			idx, _, _ := Build(1, 4, 2, 2, []vec.Vec{{0}, {1}, {2}})
			if _, _, err := idx.Query(vec.Vec{0}, 1); err != nil {
				t.Fatal(err)
			}
		}},
		{"dim-mismatch-query", func(t *testing.T) {
			idx, _, _ := Build(3, 4, 2, 1, []vec.Vec{{1, 1, 1}})
			_, _, err := idx.Query(vec.Vec{1, 2}, 2)
			var de vec.DimError
			if !errors.Is(err, vec.ErrDimMismatch) || !errors.As(err, &de) || de.Want != 3 || de.Got != 2 {
				t.Fatalf("err=%v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestConcurrentAndAtomicPublish(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	vs, qs := synth(rng)
	idx, _, err := Build(testDim, 8, 4, testSeed, vs)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				q := qs[(w+i)%testNQ]
				rs, _, err := idx.Query(q, testK)
				if err != nil || len(rs) > testK {
					t.Errorf("query err=%v n=%d", err, len(rs))
				}
			}
		}(w)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := idx.Replace(testDim, 8, 4, testSeed, vs); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
