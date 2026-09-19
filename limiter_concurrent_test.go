package ontology

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentAllowNeverOverdraws(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(8)
	const capacity int64 = 100
	if err := l.AddTenant("t", Config{Capacity: capacity, Rate: 1000}, base); err != nil {
		t.Fatal(err)
	}

	// Drain at base, then all competing requests use the identical later
	// timestamp. That allows exactly 1000 refilled tokens with no timing race.
	if ok, err := l.Allow("t", capacity, base); !ok || err != nil {
		t.Fatal(err)
	}
	now := base.Add(time.Second)
	const goroutines = 2000

	var successes atomic.Int64
	var unexpected atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := l.Allow("t", 1, now)
			switch {
			case ok:
				successes.Add(1)
			case errors.Is(err, ErrTokensUnavailable):
			default:
				t.Errorf("unexpected error: %v", err)
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if successes.Load() != capacity {
		t.Fatalf("issued %d tokens, want exactly %d", successes.Load(), capacity)
	}
	if unexpected.Load() != 0 {
		t.Fatalf("unexpected errors: %d", unexpected.Load())
	}
	got, err := l.AvailableTokens("t", now)
	if err != nil || got != 0 {
		t.Fatalf("remaining = %d, %v", got, err)
	}
}

func TestConcurrentReapAndAllowIsSafe(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(16)
	const tenantCount = 400
	for i := range tenantCount {
		name := "tenant-" + strconv.Itoa(i)
		if err := l.AddTenant(name, Config{Capacity: 2, Rate: 1}, base); err != nil {
			t.Fatal(err)
		}
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				l.ReapInactive(base)
			}
		}
	}()

	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				name := "tenant-" + strconv.Itoa((worker*37+i)%tenantCount)
				_, _ = l.Allow(name, 1, base.Add(time.Duration(i)*time.Nanosecond))
			}
		}(worker)
	}

	close(stop)
	wg.Wait()
}
