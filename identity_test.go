package ontology

import (
	"math"
	"math/rand/v2"
	"sync"
	"testing"
)

func assertIdentity(t *testing.T, h *Histogram, where string) {
	t.Helper()
	s := h.Snapshot()
	if !s.IdentityHolds() {
		t.Fatalf("计数恒等式不成立 @%s: buckets=%v under=%d over=%d skip=%d added=%d",
			where, s.Counts, s.Under, s.Over, s.Skip, s.Added)
	}
}

// TestIdentityRandom 在随机样本流上反复断言计数恒等式。
func TestIdentityRandom(t *testing.T) {
	specs := []struct {
		lo, hi float64
		n      int
	}{
		{0, 1, 3},
		{-1, 2, 7},
		{-5, -4, 13},
		{100, 200, 32},
	}
	for si, spec := range specs {
		h, err := NewHistogram(spec.lo, spec.hi, spec.n)
		if err != nil {
			t.Fatalf("spec %d: %v", si, err)
		}
		rng := rand.New(rand.NewPCG(uint64(si+1), uint64(si+99)))
		for step := 0; step < 20000; step++ {
			switch step % 17 {
			case 0:
				_ = h.Add(math.NaN())
			case 1:
				_ = h.Add(math.Inf(-1))
			case 2:
				_ = h.Add(math.Inf(1))
			default:
				// 覆盖区间内外。
				x := spec.lo - 1 + rng.Float64()*(spec.hi-spec.lo+2)
				_ = h.Add(x)
			}
			if step%1000 == 0 {
				assertIdentity(t, h, "random stream")
			}
		}
		assertIdentity(t, h, "random stream end")
	}
}

// TestConcurrentAdd 多协程并发 Add 不得丢样本，结束后恒等式仍成立。
func TestConcurrentAdd(t *testing.T) {
	h, err := NewHistogram(0, 1, 8)
	if err != nil {
		t.Fatalf("NewHistogram: %v", err)
	}

	const goroutines = 16
	const perG = 5000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, seed+7))
			for i := 0; i < perG; i++ {
				var x float64
				switch i % 20 {
				case 0:
					x = math.NaN()
				case 1:
					x = math.Inf(-1)
				case 2:
					x = math.Inf(1)
				default:
					x = rng.Float64()*1.2 - 0.1
				}
				_ = h.Add(x)
			}
		}(uint64(g + 1))
	}
	wg.Wait()

	s := h.Snapshot()
	if s.Added != goroutines*perG {
		t.Fatalf("丢样本：want Added=%d, got %d", goroutines*perG, s.Added)
	}
	if !s.IdentityHolds() {
		t.Fatalf("并发结束后计数恒等式不成立: buckets=%v under=%d over=%d skip=%d added=%d",
			s.Counts, s.Under, s.Over, s.Skip, s.Added)
	}
}
