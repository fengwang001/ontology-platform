package idem

import (
	"errors"
	"testing"
)

// Semantics 1: a second identical call replays the first result without
// executing fn again.
func TestReplaySameFingerprint(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	var calls int
	want := Result{Code: 201, Body: "created"}
	got, outcome, err := ex.Do("k", "fp-a", func() (Result, error) {
		calls++
		return want, nil
	})
	if err != nil || outcome != Executed || got != want {
		t.Fatalf("first call: %+v %v %v", got, outcome, err)
	}

	got, outcome, err = ex.Do("k", "fp-a", func() (Result, error) {
		calls++
		return Result{Code: 999, Body: "must not run"}, nil
	})
	if err != nil || outcome != Replayed || got != want || calls != 1 {
		t.Fatalf("replay: %+v %v %v calls=%d", got, outcome, err, calls)
	}
}

// Semantics 2: a different fingerprint on the same key is rejected, fn does
// not run, and the original record stays intact and replayable.
func TestFingerprintMismatch(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	want := Result{Code: 200, Body: "original"}
	_, _, _ = ex.Do("k", "fp-a", func() (Result, error) { return want, nil })

	called := false
	_, _, err := ex.Do("k", "fp-b", func() (Result, error) {
		called = true
		return Result{}, nil
	})
	if !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("want ErrFingerprintMismatch, got %v", err)
	}
	if called {
		t.Fatal("fn must not execute on fingerprint conflict")
	}

	got, outcome, err := ex.Do("k", "fp-a", func() (Result, error) {
		t.Fatal("fn must not run on replay after conflict")
		return Result{}, nil
	})
	if err != nil || outcome != Replayed || got != want {
		t.Fatalf("record overwritten: %+v %v %v", got, outcome, err)
	}
}

// Semantics 3a: business errors are cached and replayed identically.
func TestBusinessErrorCached(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	type bizErr struct{ code string }
	biz := &bizErr{code: "E42"}
	wantRes := Result{Code: 418, Body: "teapot"}

	var calls int
	run := func() (Result, error) {
		calls++
		return wantRes, biz
	}

	res, outcome, err := ex.Do("k", "fp", run)
	if err != biz || outcome != Executed || res != wantRes {
		t.Fatalf("first: %+v %v %v", res, outcome, err)
	}
	res, outcome, err = ex.Do("k", "fp", run)
	if err != biz || outcome != Replayed || res != wantRes || calls != 1 {
		t.Fatalf("replay: %+v %v %v calls=%d", res, outcome, err, calls)
	}

	var target *bizErr
	if !errors.As(err, &target) || target.code != "E42" {
		t.Fatalf("replayed error identity broken: %v", err)
	}
}
