package testclock_test

import (
	"sync"
	"testing"
	"time"

	"ontology/internal/testclock"
)

func TestAdvanceAndSet(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := testclock.New(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("Now() = %v, want %v", got, start)
	}
	c.Advance(time.Minute)
	if got := c.Now(); !got.Equal(start.Add(time.Minute)) {
		t.Fatalf("after Advance: Now() = %v", got)
	}
	c.Set(start)
	if got := c.Now(); !got.Equal(start) {
		t.Fatalf("after Set: Now() = %v", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := testclock.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Advance(time.Nanosecond)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = c.Now()
			}
		}()
	}
	wg.Wait()
}
