package idem

import (
	"errors"
	"testing"
)

var errInfra = errors.New("infra down")

// Semantics 3b: retriable errors are not cached; the slot is released and the
// next call executes fn again.
func TestRetriableNotCached(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	var calls int
	first := func() (Result, error) {
		calls++
		return Result{}, Retriable(errInfra)
	}
	_, _, err := ex.Do("k", "fp", first)
	if !errors.Is(err, errInfra) {
		t.Fatalf("retriable wrapper must unwrap: %v", err)
	}

	want := Result{Code: 200, Body: "recovered"}
	res, outcome, err := ex.Do("k", "fp", func() (Result, error) {
		calls++
		return want, nil
	})
	if err != nil || outcome != Executed || res != want || calls != 2 {
		t.Fatalf("retry after retriable: %+v %v %v calls=%d", res, outcome, err, calls)
	}
}

// Semantics 4: a panic releases the slot, is surfaced to the current caller as
// an error, and a later call can execute and succeed.
func TestPanicReleasesSlot(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	var calls int
	_, _, err := ex.Do("k", "fp", func() (Result, error) {
		calls++
		panic("boom")
	})
	if err == nil || err.Error() == "" {
		t.Fatalf("panic must become error, got %v", err)
	}

	res, outcome, err := ex.Do("k", "fp", func() (Result, error) {
		calls++
		return Result{Code: 200, Body: "ok"}, nil
	})
	if err != nil || outcome != Executed || res.Body != "ok" || calls != 2 {
		t.Fatalf("retry after panic: %+v %v %v calls=%d", res, outcome, err, calls)
	}
}

// A panic carrying an error value preserves errors.Is identity via Unwrap.
func TestPanicErrorValueUnwraps(t *testing.T) {
	clock := newFakeClock(1000)
	ex := newExecutor(clock, 0)

	_, _, err := ex.Do("k", "fp", func() (Result, error) {
		panic(errInfra)
	})
	if !errors.Is(err, errInfra) {
		t.Fatalf("panicked error should unwrap, got %v", err)
	}
}
