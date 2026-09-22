package ontology

import (
	"math"
	"sync"
	"testing"
)

func TestConcurrentAddCountAndMean(t *testing.T) {
	a := New()
	const goroutines = 16
	const perGoroutine = 5000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(base float64) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = a.Add(base + float64(i))
			}
		}(float64(g * perGoroutine))
	}
	wg.Wait()
	const wantCount = goroutines * perGoroutine
	if got := a.Count(); got != wantCount {
		t.Fatalf("Count = %d, want %d (lost samples under concurrency)", got, wantCount)
	}
	// The samples are exactly 0..wantCount-1, so the mean is known.
	wantMean := float64(wantCount-1) / 2
	if m := mustMean(t, a); math.Abs(m-wantMean) > 1e-6*wantMean {
		t.Fatalf("Mean = %v, want %v", m, wantMean)
	}
}

// TestConcurrentReadConsistency hammers the accumulator with adds
// while readers pull statistics. With -race this proves no torn
// state is observable; the invariant check proves a reader never
// sees a count that was bumped without its mean.
func TestConcurrentReadConsistency(t *testing.T) {
	a := New()
	done := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				m, err := a.Mean()
				if err == nil {
					if math.IsNaN(m) || m < 0 || m > 1 {
						t.Errorf("observed impossible mean %v", m)
						return
					}
					if a.Count() == 0 {
						t.Error("Mean succeeded while Count is 0")
						return
					}
				}
				_, _ = a.Variance()
				_, _ = a.SampleVariance()
			}
		}()
	}
	for i := 0; i < 20000; i++ {
		_ = a.Add(float64(i % 2))
	}
	close(done)
	wg.Wait()
}

func TestConcurrentMergeDoesNotBlockOrMutate(t *testing.T) {
	a, b := New(), New()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(dst *Accumulator) {
			defer wg.Done()
			for i := 0; i < 5000; i++ {
				_ = dst.Add(float64(i))
			}
		}([]*Accumulator{a, b}[g%2])
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m := Merge(a, b)
			_ = m.Count()
		}
	}()
	wg.Wait()
	if a.Count() != 10000 || b.Count() != 10000 {
		t.Fatalf("counts = %d, %d; want 10000, 10000", a.Count(), b.Count())
	}
}
