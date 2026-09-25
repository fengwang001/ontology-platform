package api_test

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"

	"ontology/api"
)

func sigma(p int) float64 { return 1.04 / math.Sqrt(float64(int(1)<<p)) }

// Estimate must stay within 3 sigma of the true distinct count.
func TestEstimateBound(t *testing.T) {
	for _, p := range []int{4, 8, 12, 16} {
		n := 32 << p
		t.Run(fmt.Sprintf("p=%d/n=%d", p, n), func(t *testing.T) {
			s, _ := api.New(p)
			for i := 0; i < n; i++ {
				s.Add(fmt.Sprintf("bound-%d", i))
			}
			est, _ := s.Estimate()
			if d := math.Abs(est-float64(n)) / float64(n); d > 3*sigma(p) {
				t.Errorf("est=%f n=%d rel err=%f > 3sigma", est, n, d)
			}
		})
	}
}

// Every Add keeps each register and Estimate monotonically non-decreasing.
func TestMonotonic(t *testing.T) {
	s, _ := api.New(6)
	prev := 0.0
	for i := 0; i < 3000; i++ {
		s.Add(fmt.Sprintf("mono-%d", i))
		e, _ := s.Estimate()
		if e < prev {
			t.Fatalf("estimate decreased after %d adds: %f -> %f", i, prev, e)
		}
		prev = e
	}
}

// The three failure kinds are distinguishable sentinel errors; a rejected
// operation changes nothing and the sketch stays usable.
func TestFailureNoTrace(t *testing.T) {
	for _, p := range []int{-1, 0, 3, 17, 100} {
		if _, err := api.New(p); !errors.Is(err, api.ErrPrecision) {
			t.Errorf("New(%d) = %v, want ErrPrecision", p, err)
		}
	}
	s, _ := api.New(8)
	for i := 0; i < 100; i++ {
		s.Add(fmt.Sprintf("ft-%d", i))
	}
	before, _ := s.Estimate()
	other, _ := api.New(9)
	if err := s.Merge(other); !errors.Is(err, api.ErrMismatch) {
		t.Errorf("Merge(p=8,p=9) = %v, want ErrMismatch", err)
	}
	zero := &api.Sketch{}
	_, estErr := zero.Estimate()
	for name, err := range map[string]error{
		"zero.Add": zero.Add("x"), "zero.Estimate": estErr,
		"zero.Merge": zero.Merge(s), "s.Merge(zero)": s.Merge(zero),
	} {
		if !errors.Is(err, api.ErrNotInit) {
			t.Errorf("%s = %v, want ErrNotInit", name, err)
		}
	}
	for _, pair := range [][2]error{
		{api.ErrPrecision, api.ErrMismatch}, {api.ErrMismatch, api.ErrNotInit}, {api.ErrNotInit, api.ErrPrecision},
	} {
		if errors.Is(pair[0], pair[1]) {
			t.Errorf("sentinels %v and %v not distinguishable", pair[0], pair[1])
		}
	}
	if after, _ := s.Estimate(); before != after {
		t.Errorf("rejected ops changed state: %f -> %f", before, after)
	}
	if err := s.Add("ft-more"); err != nil {
		t.Errorf("sketch unusable after rejections: %v", err)
	}
}

// Concurrent Add of the same distinct batch from N goroutines: final
// estimate within 3 sigma, concurrent Estimate reads never decrease.
func TestConcurrentAdd(t *testing.T) {
	s, _ := api.New(12)
	const G, K = 8, 3000
	done := make(chan struct{})
	var adders, reader sync.WaitGroup
	mono := true
	reader.Add(1)
	go func() {
		defer reader.Done()
		prev := 0.0
		for {
			select {
			case <-done:
				return
			default:
				if e, _ := s.Estimate(); e < prev {
					mono = false
				} else {
					prev = e
				}
			}
		}
	}()
	for g := 0; g < G; g++ {
		adders.Add(1)
		go func() {
			defer adders.Done()
			for i := 0; i < K; i++ {
				s.Add(fmt.Sprintf("conc-%d", i))
			}
		}()
	}
	adders.Wait()
	close(done)
	reader.Wait()
	if !mono {
		t.Fatal("concurrent Estimate decreased")
	}
	est, _ := s.Estimate()
	if d := math.Abs(est-K) / K; d > 3*sigma(12) {
		t.Errorf("est=%f, rel err %f > 3sigma of %d", est, d, K)
	}
}

func TestSelfCheck(t *testing.T) {
	s, _ := api.New(10)
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if err := (&api.Sketch{}).SelfCheck(); !errors.Is(err, api.ErrNotInit) {
		t.Errorf("zero SelfCheck = %v, want ErrNotInit", err)
	}
}
