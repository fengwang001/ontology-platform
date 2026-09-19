package ontology

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Under a frozen clock no refill happens, so concurrent single-token
// requests to the same tenant must never hand out more tokens than the
// bucket held at the start. Not a single token may be over-issued.
func TestConcurrentNoOverIssueFrozen(t *testing.T) {
	const capacity = int64(200)
	l := newTestLimiter(t, capacity, 10)
	now := time.Unix(42, 0)

	const goroutines, perG = 64, 500
	var granted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < perG; i++ {
				ok, err := l.Allow("hot", 1, now)
				if errors.Is(err, ErrInsufficientTokens) {
					continue
				}
				if err != nil {
					t.Errorf("unexpected err: %v", err)
					return
				}
				if ok {
					atomic.AddInt64(&granted, 1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if granted != capacity {
		t.Fatalf("granted=%d, want exactly %d", granted, capacity)
	}
	got, _ := l.Available("hot", now)
	if got != 0 {
		t.Fatalf("remaining=%d, want 0", got)
	}
}

// With the (injected) clock advancing, total grants must never exceed
// initial tokens plus everything refilled during the window.
func TestConcurrentNoOverIssueWithRefill(t *testing.T) {
	const capacity, rate = int64(100), int64(100)
	l := newTestLimiter(t, capacity, rate)
	t0 := time.Unix(0, 0)
	// Drain to zero at t0.
	for i := int64(0); i < capacity; i++ {
		if ok, _ := l.Allow("hot", 1, t0); !ok {
			t.Fatal("drain")
		}
	}

	const goroutines = 32
	const window = time.Second // at rate 100/s this adds exactly 100 tokens
	var granted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	// Every request lands at the exact same future timestamp t0+1s: the
	// bucket holds exactly 100 tokens then, so exactly 100 of the 3200
	// concurrent requests must succeed and never one more.
	now := t0.Add(window)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				ok, err := l.Allow("hot", 1, now)
				switch {
				case errors.Is(err, ErrTimeReversed):
					t.Errorf("unexpected time reversal")
					return
				case err != nil:
					if !errors.Is(err, ErrInsufficientTokens) {
						t.Errorf("unexpected err: %v", err)
						return
					}
				case ok:
					atomic.AddInt64(&granted, 1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if granted != 100 {
		t.Fatalf("granted=%d, want exactly 100 (initial 0 + 100 refilled)", granted)
	}
	got, _ := l.Available("hot", now)
	if got != 0 {
		t.Fatalf("final available=%d, want 0", got)
	}
}

// Reclamation must never corrupt or remove a bucket that a concurrent
// Allow is actively using. Run under -race this also checks the locking.
func TestConcurrentReclaimDuringAllow(t *testing.T) {
	l := newTestLimiter(t, 50, 10)
	now := time.Unix(0, 0)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = l.Allow("persistent", 1, now)
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = l.Allow("ephemeral", 1, now)
			l.ReclaimIdle(now.Add(time.Nanosecond))
		}
		close(stop)
	}()
	wg.Wait()
}

// Different tenants live on (potentially) different shards; hammering
// many of them concurrently must stay safe and keep grants per tenant
// within bounds.
func TestConcurrentDistinctTenants(t *testing.T) {
	l := newTestLimiter(t, 10, 5)
	now := time.Unix(1, 0)
	var wg sync.WaitGroup
	for tID := 0; tID < 50; tID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tenant := "tenant-" + string(rune('A'+id))
			var grants int64
			for i := 0; i < 100; i++ {
				ok, err := l.Allow(tenant, 1, now)
				switch {
				case errors.Is(err, ErrInsufficientTokens):
					// expected once the frozen bucket is empty
				case err != nil:
					t.Errorf("tenant %s: %v", tenant, err)
					return
				}
				if ok {
					grants++
				}
			}
			if grants > 10 {
				t.Errorf("tenant %s granted=%d > 10", tenant, grants)
			}
		}(tID)
	}
	wg.Wait()
}
