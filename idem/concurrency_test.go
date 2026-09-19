package idem

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleflightConcurrentCallers(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	const n = 16
	var calls atomic.Int32
	release := make(chan struct{})
	fn := func() (Result, error) {
		calls.Add(1)
		<-release
		return Result{Code: 200, Body: "shared"}, nil
	}
	var wg sync.WaitGroup
	ress := make([]Result, n)
	outs := make([]Outcome, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ress[i], outs[i], errs[i] = e.Do("k", "fp", fn)
		}(i)
	}
	// Let all goroutines pile onto the key, then release fn.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn executed %d times, want 1", got)
	}
	var waited int
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d err=%v", i, errs[i])
		}
		if ress[i] != (Result{Code: 200, Body: "shared"}) {
			t.Fatalf("caller %d res=%v", i, ress[i])
		}
		switch outs[i] {
		case Waited:
			waited++
		case Executed:
		default:
			t.Fatalf("caller %d out=%v", i, outs[i])
		}
	}
	if waited != n-1 {
		t.Fatalf("waited=%d, want %d", waited, n-1)
	}
}

func TestDifferentKeysDoNotBlock(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	release := make(chan struct{})
	done := make(chan Outcome, 1)
	go func() {
		_, out, _ := e.Do("slow", "fp", func() (Result, error) {
			<-release
			return Result{Code: 200}, nil
		})
		done <- out
	}()
	time.Sleep(20 * time.Millisecond) // ensure "slow" is in-flight
	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		res, out, err := e.Do("fast", "fp", func() (Result, error) {
			return Result{Code: 201}, nil
		})
		if err != nil || out != Executed || res.Code != 201 {
			t.Errorf("fast key: res=%v out=%v err=%v", res, out, err)
		}
	}()
	select {
	case <-fastDone:
	case <-time.After(2 * time.Second):
		t.Fatal("different key blocked behind in-flight key")
	}
	close(release)
	if out := <-done; out != Executed {
		t.Fatalf("slow key out=%v", out)
	}
}

func TestTTLExpiryUsesInjectedClock(t *testing.T) {
	clk := newFakeClock()
	e := New(clk.now, time.Minute)
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: calls}, nil
	}
	if _, _, err := e.Do("k", "fp", fn); err != nil {
		t.Fatal(err)
	}
	clk.advance(time.Minute - time.Nanosecond) // now < expires: replay
	res, out, _ := e.Do("k", "fp", fn)
	if out != Replayed || res.Code != 1 {
		t.Fatalf("before expiry: res=%v out=%v", res, out)
	}
	clk.advance(time.Nanosecond) // now == expires: expired
	res, out, _ = e.Do("k", "fp", fn)
	if out != Executed || res.Code != 2 {
		t.Fatalf("at expiry: res=%v out=%v", res, out)
	}
	if calls != 2 {
		t.Fatalf("fn executed %d times, want 2", calls)
	}
}

func TestNonPositiveTTLNeverExpires(t *testing.T) {
	clk := newFakeClock()
	e := New(clk.now, 0)
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 7}, nil
	}
	if _, _, err := e.Do("k", "fp", fn); err != nil {
		t.Fatal(err)
	}
	clk.advance(1000 * time.Hour)
	res, out, _ := e.Do("k", "fp", fn)
	if out != Replayed || res.Code != 7 || calls != 1 {
		t.Fatalf("res=%v out=%v calls=%d", res, out, calls)
	}
}

func TestInFlightNeverExpires(t *testing.T) {
	clk := newFakeClock()
	e := New(clk.now, time.Second)
	release := make(chan struct{})
	var calls atomic.Int32
	fn := func() (Result, error) {
		calls.Add(1)
		<-release
		return Result{Code: 200}, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Do("k", "fp", fn)
	}()
	time.Sleep(20 * time.Millisecond) // first call is in-flight
	clk.advance(time.Hour)            // far beyond ttl
	second := make(chan Outcome, 1)
	go func() {
		_, out, _ := e.Do("k", "fp", fn)
		second <- out
	}()
	time.Sleep(20 * time.Millisecond)
	select {
	case out := <-second:
		t.Fatalf("in-flight key expired and re-executed, out=%v", out)
	default:
	}
	close(release)
	<-done
	if out := <-second; out != Waited {
		t.Fatalf("second caller out=%v, want Waited", out)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn executed %d times, want 1", got)
	}
}
