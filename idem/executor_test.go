package idem

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
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

func TestReplaySameKeySameFingerprint(t *testing.T) {
	clk := newFakeClock()
	e := New(clk.now, time.Minute)
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 200, Body: "ok"}, nil
	}
	res, out, err := e.Do("k", "fp", fn)
	if err != nil || out != Executed || res.Code != 200 {
		t.Fatalf("first: res=%v out=%v err=%v", res, out, err)
	}
	res, out, err = e.Do("k", "fp", fn)
	if err != nil || out != Replayed || res != (Result{Code: 200, Body: "ok"}) {
		t.Fatalf("replay: res=%v out=%v err=%v", res, out, err)
	}
	if calls != 1 {
		t.Fatalf("fn executed %d times, want 1", calls)
	}
}

func TestFingerprintMismatch(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	want := Result{Code: 201, Body: "first"}
	if _, _, err := e.Do("k", "fp1", func() (Result, error) { return want, nil }); err != nil {
		t.Fatal(err)
	}
	ran := false
	_, _, err := e.Do("k", "fp2", func() (Result, error) {
		ran = true
		return Result{}, nil
	})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("err=%v, want ErrFingerprintMismatch", err)
	}
	if ran {
		t.Fatal("fn must not run on fingerprint mismatch")
	}
	// Original fingerprint still replays the original result.
	res, out, err := e.Do("k", "fp1", func() (Result, error) { return Result{}, nil })
	if err != nil || out != Replayed || res != want {
		t.Fatalf("after conflict: res=%v out=%v err=%v", res, out, err)
	}
}

func TestBusinessErrorIsCachedAndReplayed(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	bizErr := errors.New("business failure")
	calls := 0
	fn := func() (Result, error) {
		calls++
		return Result{Code: 422, Body: "bad"}, bizErr
	}
	res, _, err := e.Do("k", "fp", fn)
	if !errors.Is(err, bizErr) || res.Code != 422 {
		t.Fatalf("first: res=%v err=%v", res, err)
	}
	res, out, err := e.Do("k", "fp", fn)
	if !errors.Is(err, bizErr) || out != Replayed || res.Code != 422 {
		t.Fatalf("replay: res=%v out=%v err=%v", res, out, err)
	}
	if calls != 1 {
		t.Fatalf("fn executed %d times, want 1", calls)
	}
}

func TestRetriableErrorIsNotCached(t *testing.T) {
	e := New(newFakeClock().now, time.Minute)
	infra := errors.New("db down")
	calls := 0
	fn := func() (Result, error) {
		calls++
		if calls == 1 {
			return Result{}, Retriable(infra)
		}
		return Result{Code: 200, Body: "recovered"}, nil
	}
	_, _, err := e.Do("k", "fp", fn)
	if !errors.Is(err, infra) {
		t.Fatalf("first: err=%v", err)
	}
	res, out, err := e.Do("k", "fp", fn)
	if err != nil || out != Executed || res.Code != 200 {
		t.Fatalf("second: res=%v out=%v err=%v", res, out, err)
	}
	if calls != 2 {
		t.Fatalf("fn executed %d times, want 2", calls)
	}
}

func TestRetriableNilIsNil(t *testing.T) {
	if Retriable(nil) != nil {
		t.Fatal("Retriable(nil) must be nil")
	}
}

func TestPanicReleasesSlot(t *testing.T) {
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
		t.Fatal("panic must be converted to error")
	}
	res, out, err := e.Do("k", "fp", fn)
	if err != nil || out != Executed || res.Code != 200 {
		t.Fatalf("after panic: res=%v out=%v err=%v", res, out, err)
	}
	if calls != 2 {
		t.Fatalf("fn executed %d times, want 2", calls)
	}
}
