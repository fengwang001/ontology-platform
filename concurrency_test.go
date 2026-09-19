package ontology

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// N goroutines race on one tenant: the total tokens handed out never
// exceed what the bucket held plus what refilled meanwhile.
func TestConcurrentNoOversell(t *testing.T) {
	const capacity = 500
	l := mustLimiter(t, capacity, 0)
	now := time.Unix(16_000, 0)

	var granted atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				if d, err := l.Allow("hot", 1, now); err == nil && d.Allowed {
					granted.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := granted.Load(); got != capacity {
		t.Fatalf("granted %d tokens, want exactly %d (no oversell, no loss)", got, capacity)
	}
	if got := l.Available("hot", now); got != 0 {
		t.Fatalf("bucket has %d tokens left, want 0", got)
	}
}

// Many tenants at once: each stays within its own bucket, and the run is
// race-clean. Different tenants hash to different shards, so they do not
// serialize on one global lock.
func TestConcurrentTenantsIsolated(t *testing.T) {
	const (
		tenants  = 32
		perShard = 50
		capacity = 100
	)
	l := mustLimiter(t, capacity, 0)
	now := time.Unix(17_000, 0)

	granted := make([]atomic.Int64, tenants)
	var wg sync.WaitGroup
	for tenant := 0; tenant < tenants; tenant++ {
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(tenant int) {
				defer wg.Done()
				name := fmt.Sprintf("tenant-%d", tenant)
				for i := 0; i < perShard; i++ {
					if d, err := l.Allow(name, 1, now); err == nil && d.Allowed {
						granted[tenant].Add(1)
					}
				}
			}(tenant)
		}
	}
	wg.Wait()
	for tenant := 0; tenant < tenants; tenant++ {
		if got := granted[tenant].Load(); got != capacity {
			t.Fatalf("tenant-%d granted %d tokens, want exactly %d", tenant, got, capacity)
		}
	}
}

// Concurrent batch requests of varying sizes never oversell either.
func TestConcurrentBatchNoOversell(t *testing.T) {
	const capacity = 1_000
	l := mustLimiter(t, capacity, 0)
	now := time.Unix(18_000, 0)

	var granted atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			n := int64(g%7 + 1)
			for i := 0; i < 50; i++ {
				if d, err := l.Allow("batch", n, now); err == nil && d.Allowed {
					granted.Add(n)
				}
			}
		}(g)
	}
	wg.Wait()
	if got := granted.Load(); got > capacity {
		t.Fatalf("granted %d tokens, bucket only held %d", got, capacity)
	}
	if got := granted.Load() + l.Available("batch", now); got != capacity {
		t.Fatalf("granted+leftover=%d, want exactly %d (tokens leaked or created)", got, capacity)
	}
}
