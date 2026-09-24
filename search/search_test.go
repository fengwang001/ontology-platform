package search

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"testing"

	"ontology/vec"
)

func buildClustered(t *testing.T, n, d, seed int) []vec.Vec {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seed)))
	centers := make([]vec.Vec, 40)
	for i := range centers {
		c := make(vec.Vec, d)
		for j := range c {
			c[j] = rng.NormFloat64() * 5
		}
		centers[i] = c
	}
	xs := make([]vec.Vec, n)
	for i := range xs {
		c := centers[rng.Intn(len(centers))]
		x := make(vec.Vec, d)
		for j := range x {
			x[j] = c[j] + rng.NormFloat64()*0.35
		}
		xs[i] = x
	}
	return xs
}

func buildIndex(t *testing.T, xs []vec.Vec, d, L, b, seed int) *Index {
	t.Helper()
	ix, err := NewIndex(d, L, b, int64(seed))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		if err := ix.Add(x); err != nil {
			t.Fatal(err)
		}
	}
	ix.Publish()
	return ix
}

func TestRecallMonotonicAndBudget(t *testing.T) {
	const n, d, k, qn = 5000, 16, 10, 100
	xs := buildClustered(t, n, d, 1)
	queries := buildClustered(t, qn, d, 2)

	var prev float64 = -1
	recalls := map[int]float64{}
	for _, L := range []int{1, 2, 4, 8} {
		ix := buildIndex(t, xs, d, L, 8, 7)
		ix.resetDistances()
		var sum float64
		maxCand := 0
		for _, q := range queries {
			ap, nc, err := ix.Query(q, k)
			if err != nil {
				t.Fatal(err)
			}
			if nc > maxCand {
				maxCand = nc
			}
			ex, err := ix.BruteForce(q, k)
			if err != nil {
				t.Fatal(err)
			}
			sum += Recall(ap, ex)
		}
		rec := sum / qn
		recalls[L] = rec
		if rec < prev {
			t.Fatalf("recall not monotonic: L=%d rec=%.3f prev=%.3f", L, rec, prev)
		}
		prev = rec
		if L == 8 {
			if rec < 0.6 {
				t.Fatalf("L=8 recall %.3f below 0.6", rec)
			}
			if dc := ix.Distances(); dc > 500 {
				t.Fatalf("distance calcs %d exceed 500 (10%%)", dc)
			}
		}
		t.Logf("L=%d recall=%.3f maxCandidates=%d hashCalls=%d distances=%d",
			L, rec, maxCand, ix.HashCalls(), ix.Distances())
	}
}

func TestBitsTradeoff(t *testing.T) {
	const n, d, k, qn = 5000, 16, 10, 100
	xs := buildClustered(t, n, d, 1)
	queries := buildClustered(t, qn, d, 2)
	var prevCand = math.MaxInt
	var prevRec = 2.0
	for _, b := range []int{4, 8, 12} {
		ix := buildIndex(t, xs, d, 4, b, 7)
		var sumCand float64
		var sumRec float64
		for _, q := range queries {
			ap, nc, _ := ix.Query(q, k)
			ex, _ := ix.BruteForce(q, k)
			sumCand += float64(nc)
			sumRec += Recall(ap, ex)
		}
		ac, ar := sumCand/qn, sumRec/qn
		t.Logf("b=%d avgCandidates=%.1f recall=%.3f", b, ac, ar)
		if int(ac) >= prevCand {
			t.Fatalf("candidates not decreasing at b=%d: %.1f >= %d", b, ac, prevCand)
		}
		prevCand = int(ac)
		if ar > prevRec+1e-9 {
			t.Fatalf("recall should not rise with b: b=%d %.3f > %.3f", b, ar, prevRec)
		}
		prevRec = ar
	}
}

func TestHashCounterExact(t *testing.T) {
	d, L, b := 6, 3, 7
	xs := buildClustered(t, 137, d, 11)
	ix := buildIndex(t, xs, d, L, b, 5)
	if got, want := ix.HashCalls(), 137*L*b; got != want {
		t.Fatalf("hashCalls=%d want %d", got, want)
	}
}

func TestEdgeSemantics(t *testing.T) {
	d := 4
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"empty index K", func(t *testing.T) {
			ix := buildIndex(t, nil, d, 2, 4, 1)
			h, _, err := ix.Query(make(vec.Vec, d), 10)
			if err != nil || len(h) != 0 {
				t.Fatalf("empty query h=%v err=%v", h, err)
			}
		}},
		{"single vector", func(t *testing.T) {
			ix := buildIndex(t, []vec.Vec{{1, 2, 3, 4}}, d, 2, 4, 1)
			h, _, _ := ix.Query(vec.Vec{1, 2, 3, 5}, 5)
			if len(h) != 1 || h[0].ID != 0 {
				t.Fatalf("h=%v", h)
			}
		}},
		{"K greater than N", func(t *testing.T) {
			ix := buildIndex(t, []vec.Vec{{0, 0, 0, 0}, {1, 0, 0, 0}}, d, 2, 4, 1)
			h, _, _ := ix.Query(vec.Vec{2, 0, 0, 0}, 10)
			if len(h) != 2 {
				t.Fatalf("len=%d want 2", len(h))
			}
		}},
		{"all identical recall 1", func(t *testing.T) {
			xs := make([]vec.Vec, 300)
			for i := range xs {
				xs[i] = vec.Vec{1, 1, 1, 1}
			}
			ix := buildIndex(t, xs, d, 4, 8, 3)
			ix.resetDistances()
			ap, nc, _ := ix.Query(vec.Vec{1, 1, 1, 1}, 10)
			ex, _ := ix.BruteForce(vec.Vec{1, 1, 1, 1}, 10)
			if Recall(ap, ex) != 1 || nc != 300 {
				t.Fatalf("recall=%v cand=%d", Recall(ap, ex), nc)
			}
		}},
		{"nan and inf skipped/countable", func(t *testing.T) {
			ix, _ := NewIndex(d, 2, 4, 1)
			if err := ix.Add(vec.Vec{1, math.NaN(), 0, 0}); !errors.Is(err, vec.ErrInvalidVector) {
				t.Fatalf("nan err=%v", err)
			}
			if err := ix.Add(vec.Vec{1, math.Inf(1), 0, 0}); !errors.Is(err, vec.ErrInvalidVector) {
				t.Fatalf("inf err=%v", err)
			}
			if ix.Skipped() != 2 {
				t.Fatalf("skipped=%d", ix.Skipped())
			}
		}},
		{"dim one works", func(t *testing.T) {
			ix := buildIndex(t, []vec.Vec{{0}, {1}, {2}}, 1, 2, 4, 1)
			h, _, err := ix.Query(vec.Vec{3}, 2)
			if err != nil || len(h) != 2 || h[0].ID != 2 {
				t.Fatalf("h=%v err=%v", h, err)
			}
		}},
		{"query dim mismatch classified", func(t *testing.T) {
			ix := buildIndex(t, []vec.Vec{{1, 2, 3, 4}}, d, 2, 4, 1)
			_, _, err := ix.Query(vec.Vec{1, 2}, 3)
			if !errors.Is(err, vec.ErrDimMismatch) {
				t.Fatalf("err=%v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
}

func TestNotReadyAndConcurrency(t *testing.T) {
	ix, _ := NewIndex(4, 4, 8, 1)
	if _, _, err := ix.Query(vec.Vec{0, 0, 0, 0}, 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("before publish err=%v", err)
	}
	xs := buildClustered(t, 2000, 4, 9)
	var wg sync.WaitGroup
	for _, x := range xs {
		x := x
		wg.Add(1)
		go func() { defer wg.Done(); _ = ix.Add(x) }()
	}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := ix.Query(vec.Vec{0, 0, 0, 0}, 5); err != nil &&
				!errors.Is(err, ErrNotReady) {
				t.Errorf("query err=%v", err)
			}
		}()
	}
	wg.Wait()
	ix.Publish()

	var wg2 sync.WaitGroup
	for i := 0; i < 32; i++ {
		q := buildClustered(t, 1, 4, int64(100+i))[0]
		wg2.Add(1)
		go func() { defer wg2.Done(); if _, _, err := ix.Query(q, 10); err != nil { t.Error(err) } }()
	}
	wg2.Wait()
}

func BenchmarkClustered(b *testing.B) {
	for n := 100; n <= 5000; n *= 2 {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			_ = buildClustered(nil, n, 16, 1)
		})
	}
}
