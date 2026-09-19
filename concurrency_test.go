package ontology

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentAllowNeverOvershoots(t *testing.T) {
	now := time.Unix(1200, 0)
	limiter := testLimiter(t, Config{Rate: 10, Capacity: 10, ShardCount: 4})

	const goroutines = 200
	var allowed atomic.Int64
	var started sync.WaitGroup
	var release sync.WaitGroup
	var done sync.WaitGroup
	started.Add(goroutines)
	release.Add(1)
	done.Add(goroutines)

	for range goroutines {
		go func() {
			defer done.Done()
			started.Done()
			release.Wait()
			if err := limiter.Allow("same", 1, now); err == nil {
				allowed.Add(1)
			}
		}()
	}

	started.Wait()
	release.Done()
	done.Wait()

	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed tokens = %d, want 10", got)
	}
	assertAvailable(t, limiter, "same", now, 0)
}

func TestConcurrentTenantsUseIndependentShardLocks(t *testing.T) {
	now := time.Unix(1300, 0)
	limiter := testLimiter(t, Config{Rate: 10, Capacity: 10, ShardCount: 16})

	const tenants = 16
	var wg sync.WaitGroup
	wg.Add(tenants)
	for index := range tenants {
		go func(index int) {
			defer wg.Done()
			tenant := "tenant-" + string(rune('a'+index))
			for range 100 {
				_ = limiter.Allow(tenant, 1, now)
			}
		}(index)
	}
	wg.Wait()

	if got := limiter.ActiveTenants(); got != tenants {
		t.Fatalf("ActiveTenants() = %d, want %d", got, tenants)
	}
}

func TestEvictionDoesNotDisturbConcurrentlyActiveTenant(t *testing.T) {
	base := time.Unix(1400, 0)
	limiter := testLimiter(t, Config{Rate: 1000, Capacity: 100, IdleTTL: time.Second})
	assertNoError(t, limiter.Allow("busy", 100, base))

	const requests = 100
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for step := 1; step <= requests; step++ {
			now := base.Add(time.Duration(step) * time.Millisecond)
			_ = limiter.Allow("busy", 1, now)
		}
	}()
	go func() {
		defer wg.Done()
		for step := 1; step <= requests; step++ {
			limiter.EvictInactive(base.Add(time.Duration(step) * time.Millisecond))
		}
	}()
	wg.Wait()

	assertAvailable(t, limiter, "busy", base.Add(requests*time.Millisecond), 0)
}
