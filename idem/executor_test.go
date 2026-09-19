package idem

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Semantics 5: concurrent calls with the same key single-flight; exactly one
// execution, everyone else waits and shares the result.
func TestSingleFlight(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Minute)
	const n = 16
	var calls atomic.Int32
	fn := func() (Result, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return Result{Code: 201, Body: "made"}, nil
	}

	var wg sync.WaitGroup
	ocs := make([]Outcome, n)
	res := make([]Result, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i], ocs[i], errs[i] = e.Do("k", "fp", fn)
		}()
	}
	wg.Wait()

	var executed, waited int
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if res[i] != (Result{Code: 201, Body: "made"}) {
			t.Fatalf("call %d got %+v", i, res[i])
		}
		switch ocs[i] {
		case Executed:
			executed++
		case Waited:
			waited++
		default:
			t.Fatalf("call %d outcome %v, want Executed or Waited", i, ocs[i])
		}
	}
	if calls.Load() != 1 || executed != 1 || waited != n-1 {
		t.Fatalf("calls=%d executed=%d waited=%d, want 1/1/%d",
			calls.Load(), executed, waited, n-1)
	}
}

// Semantics 6: a slow fn on one key must not block other keys.
func TestDifferentKeysDoNotBlock(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Minute)
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		e.Do("slow", "fp", func() (Result, error) {
			close(started)
			<-release
			return Result{Code: 200}, nil
		})
	}()
	<-started

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, _, err := e.Do("fast", "fp", okFn(200, "fast", new(int))); err != nil {
			t.Errorf("fast key: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Do on a different key blocked behind an in-flight key")
	}
	close(release)
}

// Semantics 7: expiry is completion+ttl, left-closed right-open, measured on
// the injected clock.
func TestTTLExpiryUsesInjectedClock(t *testing.T) {
	c := newClock()
	e := New(c.now, 10*time.Second)
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: calls}, nil
	}

	if _, _, err := e.Do("k", "fp", fn); err != nil {
		t.Fatalf("first call: %v", err)
	}
	c.advance(9*time.Second + 999*time.Millisecond)
	res, oc, _ := e.Do("k", "fp", fn)
	if oc != Replayed || res.Code != 1 {
		t.Fatalf("before expiry: oc=%v res=%+v, want Replayed code 1", oc, res)
	}
	c.advance(time.Millisecond) // exactly at completion+ttl
	res, oc, _ = e.Do("k", "fp", fn)
	if oc != Executed || res.Code != 2 {
		t.Fatalf("at expiry: oc=%v res=%+v, want Executed code 2", oc, res)
	}
	if calls != 2 {
		t.Fatalf("fn executed %d times, want 2", calls)
	}
}

// Semantics 7 (non-expiring): ttl <= 0 keeps records forever.
func TestNonPositiveTTLNeverExpires(t *testing.T) {
	c := newClock()
	e := New(c.now, 0)
	calls := 0
	if _, _, err := e.Do("k", "fp", okFn(200, "ok", &calls)); err != nil {
		t.Fatalf("first call: %v", err)
	}
	c.advance(1000 * time.Hour)
	if _, oc, _ := e.Do("k", "fp", okFn(200, "ok", &calls)); oc != Replayed {
		t.Fatalf("oc = %v, want Replayed", oc)
	}
	if calls != 1 {
		t.Fatalf("fn executed %d times, want 1", calls)
	}
}

// Semantics 8: an in-flight key never expires, no matter how far the clock
// advances while fn runs.
func TestInFlightNeverExpires(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Second)
	release := make(chan struct{})
	started := make(chan struct{})
	var calls atomic.Int32

	first := make(chan Outcome, 1)
	go func() {
		_, oc, _ := e.Do("k", "fp", func() (Result, error) {
			calls.Add(1)
			close(started)
			<-release
			return Result{Code: 200, Body: "done"}, nil
		})
		first <- oc
	}()
	<-started
	c.advance(time.Hour) // far past ttl while still in-flight

	second := make(chan Outcome, 1)
	go func() {
		res, oc, err := e.Do("k", "fp", func() (Result, error) {
			calls.Add(1)
			return Result{Code: 500}, nil
		})
		if err != nil || res.Code != 200 {
			t.Errorf("waiter got res=%+v err=%v", res, err)
		}
		second <- oc
	}()
	select {
	case oc := <-second:
		t.Fatalf("second call returned %v while first was in-flight", oc)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if oc := <-first; oc != Executed {
		t.Fatalf("first call oc = %v, want Executed", oc)
	}
	if oc := <-second; oc != Waited {
		t.Fatalf("second call oc = %v, want Waited", oc)
	}
	if calls.Load() != 1 {
		t.Fatalf("fn executed %d times, want 1", calls.Load())
	}
}
