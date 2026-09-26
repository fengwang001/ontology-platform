package api_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/wrs"
)

func seededRNG(seed int) func(int) float64 {
	return func(i int) float64 {
		return float64((i*1103515245+seed*12345)%1000003+1) / 1000004.0
	}
}

func genItems(n int) []api.Item {
	items := make([]api.Item, n)
	for i := range items {
		items[i] = api.Item{Val: fmt.Sprintf("v%05d", i), Weight: float64(i%9 + 1)}
	}
	return items
}

// TestSampleSizeMinKN pins invariant 1: |sample| == min(k, N).
func TestSampleSizeMinKN(t *testing.T) {
	for _, c := range []struct{ k, n int }{{1, 1}, {3, 3}, {5, 2}, {2, 17}, {10, 1000}, {4, 4}} {
		s, err := api.New(c.k, seededRNG(c.k))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Feed(genItems(c.n)); err != nil {
			t.Fatal(err)
		}
		want := c.k
		if c.n < c.k {
			want = c.n
		}
		if s.Size() != want || len(s.Sample()) != want {
			t.Errorf("k=%d n=%d: size=%d sample=%d, want %d", c.k, c.n, s.Size(), len(s.Sample()), want)
		}
	}
}

// TestFirstKAllRetained pins invariant 2: when N <= k everything is kept.
func TestFirstKAllRetained(t *testing.T) {
	for _, c := range []struct{ k, n int }{{1, 1}, {4, 3}, {10, 10}, {7, 1}} {
		s, _ := api.New(c.k, seededRNG(c.n))
		items := genItems(c.n)
		if err := s.Feed(items); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, it := range s.Sample() {
			got[it.Val] = true
		}
		for _, it := range items {
			if !got[it.Val] {
				t.Errorf("k=%d n=%d: %s missing from sample", c.k, c.n, it.Val)
			}
		}
	}
}

// TestMatchesOfflineReference pins invariant 3: online top-k equals the
// naive offline top-k under the same injected U sequence, element by element.
func TestMatchesOfflineReference(t *testing.T) {
	for _, c := range []struct{ k, n, seed int }{{1, 5, 1}, {2, 5, 2}, {3, 30, 3}, {8, 500, 4}, {5, 5, 5}} {
		rng := seededRNG(c.seed)
		s, _ := api.New(c.k, rng)
		items := genItems(c.n)
		if err := s.Feed(items); err != nil {
			t.Fatal(err)
		}
		type keyed struct {
			val string
			key float64
		}
		ks := make([]keyed, c.n)
		for i, it := range items {
			ks[i] = keyed{it.Val, wrs.Key(rng(i+1), it.Weight)}
		}
		for i := range ks { // selection sort, key descending
			for j := i + 1; j < len(ks); j++ {
				if ks[j].key > ks[i].key {
					ks[i], ks[j] = ks[j], ks[i]
				}
			}
		}
		got := s.Sample()
		for i := range got {
			if got[i].Val != ks[i].val {
				t.Errorf("k=%d n=%d seed=%d: pos %d = %s, offline wants %s", c.k, c.n, c.seed, i, got[i].Val, ks[i].val)
			}
		}
	}
}

// TestConcurrentReadersSeeSameSample: many goroutines read one filled
// instance; every reader must observe the identical sample. No sleeps.
func TestConcurrentReadersSeeSameSample(t *testing.T) {
	s, _ := api.New(7, seededRNG(9))
	if err := s.Feed(genItems(300)); err != nil {
		t.Fatal(err)
	}
	want := s.Sample()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got := s.Sample()
			if len(got) != len(want) || s.Size() != len(want) || api.SelfCheck() != nil {
				errs <- "read mismatch"
				return
			}
			for i := range got {
				if got[i] != want[i] {
					errs <- "sample mismatch"
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestSelfCheck: the built-in self check must pass.
func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
