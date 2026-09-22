package limiter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentNoOvershoot hammers Allow from many goroutines and checks
// that granted tokens never exceed what the buckets could possibly hold,
// at both the tenant and the global level. Run with -race.
func TestConcurrentNoOvershoot(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	const (
		tenants      = 5
		tenantCap    = 200.0
		globalCap    = 500.0
		workers      = 32
		triesPerWork = 200
	)
	// Refill rates are tiny so granted tokens are bounded by capacity.
	l := newLimiter(t, c, globalCap, 1e-9)
	ids := make([]string, tenants)
	for i := range ids {
		ids[i] = string(rune('a' + i))
		mustRegister(t, l, ids[i], tenantCap, 1e-9)
	}
	granted := make([]atomic.Int64, tenants)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < triesPerWork; i++ {
				tIdx := (w + i) % tenants
				if err := l.Allow(ids[tIdx], 1); err == nil {
					granted[tIdx].Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	var total int64
	for i := range granted {
		got := granted[i].Load()
		if float64(got) > tenantCap {
			t.Fatalf("tenant %s granted %d > capacity %v", ids[i], got, tenantCap)
		}
		total += got
	}
	if float64(total) > globalCap {
		t.Fatalf("globally granted %d > capacity %v", total, globalCap)
	}
	if total != int64(globalCap) {
		t.Fatalf("granted %d, want exactly global capacity %v", total, globalCap)
	}
}

// TestConcurrentMixedOps runs Allow alongside Stats, UpdateQuota,
// Unregister and Register to shake out data races.
func TestConcurrentMixedOps(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 1000, 1)
	mustRegister(t, l, "hot", 100, 1)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = l.Allow("hot", 1)
				_, _, _, _ = l.Stats("hot")
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = l.UpdateQuota("hot", mustQuota(t, 50+float64(i%3), 1))
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = l.Unregister("tmp")
			_ = l.Register("tmp", mustQuota(t, 10, 1))
		}
	}()
	wg.Wait()
}
