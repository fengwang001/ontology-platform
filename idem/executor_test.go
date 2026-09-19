package idem

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for TTL tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestReplaySameKeyAndFingerprint(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 200, Body: "ok"}, nil
	}
	r1, o1, err1 := e.Do("k", "fp", fn)
	r2, o2, err2 := e.Do("k", "fp", fn)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v %v", err1, err2)
	}
	if o1 != Executed || o2 != Replayed {
		t.Fatalf("outcomes = %v, %v; want Executed, Replayed", o1, o2)
	}
	if r1 != r2 || r2 != (Result{Code: 200, Body: "ok"}) {
		t.Fatalf("results = %+v, %+v", r1, r2)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1", calls)
	}
}

func TestFingerprintMismatch(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 201, Body: "first"}, nil
	}
	if _, _, err := e.Do("k", "fp-a", fn); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.Do("k", "fp-b", fn)
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want ErrFingerprintMismatch", err)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1 (conflict must not execute)", calls)
	}
	// The original record must survive the conflict.
	r, o, err := e.Do("k", "fp-a", fn)
	if err != nil || o != Replayed || r != (Result{Code: 201, Body: "first"}) {
		t.Fatalf("after conflict: r=%+v o=%v err=%v, want original replay", r, o, err)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1", calls)
	}
}

func TestBusinessErrorIsCached(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	bizErr := errors.New("business failure")
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 422, Body: "bad"}, bizErr
	}
	r1, _, err1 := e.Do("k", "fp", fn)
	r2, o2, err2 := e.Do("k", "fp", fn)
	if !errors.Is(err1, bizErr) || !errors.Is(err2, bizErr) {
		t.Fatalf("errors not replayed: %v %v", err1, err2)
	}
	if o2 != Replayed || r1 != r2 || r2.Code != 422 {
		t.Fatalf("r1=%+v r2=%+v o2=%v", r1, r2, o2)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1", calls)
	}
}

func TestRetriableErrorIsNotCached(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	calls := 0
	fn := func() (Result, error) {
		calls++
		if calls == 1 {
			return Result{}, Retriable(errors.New("db down"))
		}
		return Result{Code: 200, Body: "recovered"}, nil
	}
	_, _, err := e.Do("k", "fp", fn)
	var rerr error = err
	if err == nil || !isRetriable(rerr) {
		t.Fatalf("first err = %v, want retriable", err)
	}
	r, o, err := e.Do("k", "fp", fn)
	if err != nil || o != Executed || r.Code != 200 {
		t.Fatalf("second: r=%+v o=%v err=%v, want re-execution", r, o, err)
	}
	if calls != 2 {
		t.Fatalf("fn ran %d times, want 2", calls)
	}
}

func TestPanicDoesNotPoisonKey(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	calls := 0
	fn := func() (Result, error) {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return Result{Code: 200, Body: "fine"}, nil
	}
	_, _, err := e.Do("k", "fp", fn)
	if err == nil {
		t.Fatal("panic must surface as error to the current caller")
	}
	r, o, err := e.Do("k", "fp", fn)
	if err != nil || o != Executed || r.Code != 200 {
		t.Fatalf("after panic: r=%+v o=%v err=%v, want clean re-execution", r, o, err)
	}
	if calls != 2 {
		t.Fatalf("fn ran %d times, want 2", calls)
	}
}

func TestTTLBoundaryHalfOpen(t *testing.T) {
	clock := newFakeClock()
	e := New(clock.now, 10*time.Second)
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 200 + calls, Body: "v"}, nil
	}
	r1, _, _ := e.Do("k", "fp", fn) // completes at t0, expires at t0+10s
	clock.advance(10*time.Second - time.Nanosecond)
	r2, o2, _ := e.Do("k", "fp", fn) // now < expires: replay
	if o2 != Replayed || r2 != r1 {
		t.Fatalf("before expiry: o=%v r=%+v, want replay of %+v", o2, r2, r1)
	}
	clock.advance(time.Nanosecond) // now == expires: expired
	r3, o3, _ := e.Do("k", "fp", fn)
	if o3 != Executed || r3 == r1 {
		t.Fatalf("at expiry: o=%v r=%+v, want fresh execution", o3, r3)
	}
	if calls != 2 {
		t.Fatalf("fn ran %d times, want 2", calls)
	}
}

func TestNonPositiveTTLNeverExpires(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		clock := newFakeClock()
		e := New(clock.now, ttl)
		calls := 0
		fn := func() (Result, error) {
			calls++
			return Result{Code: 200}, nil
		}
		e.Do("k", "fp", fn)
		clock.advance(1000 * time.Hour)
		_, o, _ := e.Do("k", "fp", fn)
		if o != Replayed || calls != 1 {
			t.Fatalf("ttl=%v: o=%v calls=%d, want replay forever", ttl, o, calls)
		}
	}
}
