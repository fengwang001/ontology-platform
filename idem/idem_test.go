package idem

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// clock is a manually advanced, goroutine-safe time source for tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Unix(1_000_000, 0)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func okFn(code int, body string, calls *int) func() (Result, error) {
	return func() (Result, error) {
		*calls++
		return Result{Code: code, Body: body}, nil
	}
}

// Semantics 1: same key + fingerprint replays without re-executing.
func TestReplaySameKeyAndFingerprint(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Minute)
	calls := 0
	fn := okFn(200, "created", &calls)

	res1, oc1, err1 := e.Do("k", "fp", fn)
	res2, oc2, err2 := e.Do("k", "fp", fn)

	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v %v", err1, err2)
	}
	if oc1 != Executed || oc2 != Replayed {
		t.Fatalf("outcomes = %v, %v; want Executed, Replayed", oc1, oc2)
	}
	if res1 != res2 || res2 != (Result{Code: 200, Body: "created"}) {
		t.Fatalf("results differ: %+v vs %+v", res1, res2)
	}
	if calls != 1 {
		t.Fatalf("fn executed %d times, want 1", calls)
	}
}

// Semantics 2: fingerprint mismatch is a sentinel error, never executes fn,
// and never overwrites the stored record.
func TestFingerprintMismatch(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Minute)
	calls := 0
	fn := okFn(200, "first", &calls)

	if _, _, err := e.Do("k", "fp-a", fn); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, _, err := e.Do("k", "fp-b", fn)
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want ErrFingerprintMismatch", err)
	}
	if calls != 1 {
		t.Fatalf("fn executed %d times on mismatch, want 1", calls)
	}
	res, oc, err := e.Do("k", "fp-a", fn)
	if err != nil || oc != Replayed || res != (Result{Code: 200, Body: "first"}) {
		t.Fatalf("record was overwritten: res=%+v oc=%v err=%v", res, oc, err)
	}
}

// Semantics 3a: ordinary business errors are cached and replayed.
func TestBusinessErrorIsCached(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Minute)
	bizErr := errors.New("insufficient funds")
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 422, Body: "nope"}, bizErr
	}

	res1, _, err1 := e.Do("k", "fp", fn)
	res2, oc2, err2 := e.Do("k", "fp", fn)

	if !errors.Is(err1, bizErr) || !errors.Is(err2, bizErr) {
		t.Fatalf("errors not replayed: %v %v", err1, err2)
	}
	if oc2 != Replayed || res1 != res2 || res2.Code != 422 {
		t.Fatalf("bad replay: %+v/%+v oc=%v", res1, res2, oc2)
	}
	if calls != 1 {
		t.Fatalf("fn executed %d times, want 1", calls)
	}
}

// Semantics 3b: retriable errors are never cached and release the slot.
func TestRetriableErrorIsNotCached(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Minute)
	infra := errors.New("connection reset")
	calls := 0
	fn := func() (Result, error) {
		calls++
		if calls == 1 {
			return Result{}, Retriable(infra)
		}
		return Result{Code: 200, Body: "ok"}, nil
	}

	_, _, err1 := e.Do("k", "fp", fn)
	if !errors.Is(err1, infra) {
		t.Fatalf("err1 = %v, want wrapped infra error", err1)
	}
	res2, oc2, err2 := e.Do("k", "fp", fn)
	if err2 != nil || oc2 != Executed || res2.Code != 200 {
		t.Fatalf("second call: res=%+v oc=%v err=%v", res2, oc2, err2)
	}
	if calls != 2 {
		t.Fatalf("fn executed %d times, want 2", calls)
	}
}

// Semantics 4: a panic does not poison the key; the caller gets an error and
// the next call re-executes.
func TestPanicReleasesSlot(t *testing.T) {
	c := newClock()
	e := New(c.now, time.Minute)
	calls := 0
	fn := func() (Result, error) {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return Result{Code: 200, Body: "recovered"}, nil
	}

	if _, _, err := e.Do("k", "fp", fn); err == nil {
		t.Fatal("expected panic converted to error")
	}
	res, oc, err := e.Do("k", "fp", fn)
	if err != nil || oc != Executed || res.Body != "recovered" {
		t.Fatalf("after panic: res=%+v oc=%v err=%v", res, oc, err)
	}
	if calls != 2 {
		t.Fatalf("fn executed %d times, want 2", calls)
	}
}
