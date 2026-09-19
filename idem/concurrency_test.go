package idem

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleFlight(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	const n = 32
	var calls atomic.Int32
	release := make(chan struct{})
	fn := func() (Result, error) {
		calls.Add(1)
		<-release // hold the slot so all callers pile up
		return Result{Code: 200, Body: "shared"}, nil
	}
	var wg sync.WaitGroup
	results := make([]Result, n)
	outcomes := make([]Outcome, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], outcomes[i], errs[i] = e.Do("k", "fp", fn)
		}(i)
	}
	// Give every goroutine a chance to block, then release the leader.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want exactly 1", got)
	}
	var executed, waited int
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d got error %v", i, errs[i])
		}
		if results[i] != (Result{Code: 200, Body: "shared"}) {
			t.Fatalf("caller %d got %+v, want shared result", i, results[i])
		}
		switch outcomes[i] {
		case Executed:
			executed++
		case Waited:
			waited++
		default:
			t.Fatalf("caller %d got outcome %v", i, outcomes[i])
		}
	}
	if executed != 1 || waited != n-1 {
		t.Fatalf("executed=%d waited=%d, want 1 and %d", executed, waited, n-1)
	}
}

func TestDifferentKeysDoNotBlock(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	release := make(chan struct{})
	slow := func() (Result, error) {
		<-release
		return Result{Code: 200, Body: "slow"}, nil
	}
	done := make(chan struct{})
	go func() {
		e.Do("slow-key", "fp", slow)
		close(done)
	}()
	// Wait until the slow fn is actually in flight.
	time.Sleep(20 * time.Millisecond)

	fastDone := make(chan Result, 1)
	go func() {
		r, _, _ := e.Do("fast-key", "fp", func() (Result, error) {
			return Result{Code: 200, Body: "fast"}, nil
		})
		fastDone <- r
	}()
	select {
	case r := <-fastDone:
		if r.Body != "fast" {
			t.Fatalf("fast key returned %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fast key blocked behind slow key")
	}
	close(release)
	<-done
}

func TestInFlightNeverExpires(t *testing.T) {
	clock := newFakeClock()
	e := New(clock.now, time.Second) // tiny TTL
	release := make(chan struct{})
	var calls atomic.Int32
	fn := func() (Result, error) {
		calls.Add(1)
		<-release
		return Result{Code: 200, Body: "once"}, nil
	}
	leaderDone := make(chan Outcome, 1)
	go func() {
		_, o, _ := e.Do("k", "fp", fn)
		leaderDone <- o
	}()
	time.Sleep(20 * time.Millisecond) // leader is now in flight
	clock.advance(time.Hour)          // far past the TTL while in flight

	waiterDone := make(chan Outcome, 1)
	go func() {
		_, o, _ := e.Do("k", "fp", fn)
		waiterDone <- o
	}()
	select {
	case <-waiterDone:
		t.Fatal("waiter returned while fn still in flight")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if o := <-leaderDone; o != Executed {
		t.Fatalf("leader outcome = %v, want Executed", o)
	}
	if o := <-waiterDone; o != Waited {
		t.Fatalf("waiter outcome = %v, want Waited (no re-execution)", o)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times, want 1 despite TTL passing in flight", got)
	}
}
