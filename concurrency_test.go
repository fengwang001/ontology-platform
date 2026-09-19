package ontology

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentAllowDoesNotOverIssue(t *testing.T) {
	now := time.Unix(0, 0)
	const capacity = int64(100)
	const requests = 1000
	limiter := NewLimiter(capacity, 1_000_000)

	var started sync.WaitGroup
	var begin sync.WaitGroup
	var finished sync.WaitGroup
	var granted atomic.Int64
	started.Add(requests)
	begin.Add(1)
	finished.Add(requests)

	for i := 0; i < requests; i++ {
		go func() {
			defer finished.Done()
			started.Done()
			begin.Wait()
			ok, err := limiter.Allow("tenant", 1, now)
			if err != nil {
				var insufficient *InsufficientError
				if errors.As(err, &insufficient) {
					return
				}
				t.Errorf("Allow returned error: %v", err)
				return
			}
			if ok {
				granted.Add(1)
			}
		}()
	}

	started.Wait()
	begin.Done()
	finished.Wait()

	if got := granted.Load(); got != capacity {
		t.Fatalf("granted = %d, want exactly %d", got, capacity)
	}
	if got, err := limiter.Available("tenant", now); err != nil || got != 0 {
		t.Fatalf("available = %d, want 0", got)
	}
}

func TestEvictionIsSafeDuringConcurrentAccess(t *testing.T) {
	start := time.Unix(0, 0)
	limiter := NewLimiter(10, 10)
	var wg sync.WaitGroup
	now := start.Add(time.Hour)

	for i := 0; i < 16; i++ {
		if _, err := limiter.Allow("idle-"+string(rune('a'+i)), 1, start); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := limiter.Allow("active", 1, start); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, err := limiter.Allow("active", 1, now); err != nil && !errors.Is(err, ErrTimeReversed) {
					var insufficient *InsufficientError
					if !errors.As(err, &insufficient) {
						t.Errorf("Allow returned unexpected error: %v", err)
					}
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			limiter.EvictIdle(time.Minute, now)
		}
	}()

	wg.Wait()
	if got := limiter.ActiveTenants(); got != 1 {
		t.Fatalf("active tenants = %d, want only active tenant", got)
	}
}
