package ontology

import (
	"sync"
	"testing"
	"time"
)

// Tenants are fully isolated: draining one never touches another.
func TestTenantIsolation(t *testing.T) {
	l := mustLimiter(t, 5, 1)
	base := time.Unix(12_000, 0)
	drain(t, l, "a", base)
	if d, _ := l.Allow("a", 1, base); d.Allowed {
		t.Fatal("tenant a should be empty")
	}
	if got := l.Available("b", base); got != 5 {
		t.Fatalf("tenant b affected by a: available=%d, want 5", got)
	}
	d, err := l.Allow("b", 5, base)
	if err != nil || !d.Allowed {
		t.Fatalf("tenant b cannot spend its own tokens: %+v err=%v", d, err)
	}
}

func TestActiveTenants(t *testing.T) {
	l := mustLimiter(t, 5, 1)
	base := time.Unix(13_000, 0)
	if got := l.ActiveTenants(); got != 0 {
		t.Fatalf("fresh limiter tracks %d tenants, want 0", got)
	}
	for _, tenant := range []string{"a", "b", "c"} {
		l.Allow(tenant, 1, base)
	}
	if got := l.ActiveTenants(); got != 3 {
		t.Fatalf("got %d active tenants, want 3", got)
	}
	// Querying an unknown tenant must not create one.
	l.Available("nobody", base)
	if got := l.ActiveTenants(); got != 3 {
		t.Fatalf("Available created a tenant: got %d, want 3", got)
	}
}

// Idle tenants are reclaimed; a reclaimed tenant restarts with a full
// bucket, while recently active tenants are kept.
func TestReclaimIdle(t *testing.T) {
	l := mustLimiter(t, 5, 0)
	base := time.Unix(14_000, 0)
	l.Allow("old", 5, base)
	l.Allow("fresh", 5, base.Add(9*time.Minute))
	if got := l.ActiveTenants(); got != 2 {
		t.Fatalf("got %d tenants, want 2", got)
	}
	removed := l.ReclaimIdle(base.Add(10*time.Minute), 5*time.Minute)
	if removed != 1 {
		t.Fatalf("reclaimed %d tenants, want 1", removed)
	}
	if got := l.ActiveTenants(); got != 1 {
		t.Fatalf("got %d tenants after reclaim, want 1", got)
	}
	// "old" reappears with a full bucket.
	d, err := l.Allow("old", 5, base.Add(10*time.Minute))
	if err != nil || !d.Allowed {
		t.Fatalf("reclaimed tenant did not restart full: %+v err=%v", d, err)
	}
	// "fresh" was never reclaimed and is still empty.
	if d, _ := l.Allow("fresh", 1, base.Add(10*time.Minute)); d.Allowed {
		t.Fatal("active tenant wrongly reclaimed")
	}
}

// Reclaiming while other goroutines hammer the limiter is safe (run with
// -race) and never breaks in-flight requests.
func TestReclaimConcurrentWithTraffic(t *testing.T) {
	l := mustLimiter(t, 1_000, 0)
	base := time.Unix(15_000, 0)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			tenant := string(rune('a' + g))
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				l.Allow(tenant, 1, base.Add(time.Duration(i)*time.Millisecond))
			}
		}(g)
	}
	for i := 0; i < 50; i++ {
		l.ReclaimIdle(base.Add(time.Duration(i)*time.Millisecond), 0)
	}
	close(stop)
	wg.Wait()
}
