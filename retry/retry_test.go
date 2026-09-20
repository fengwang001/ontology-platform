package retry

import (
	"errors"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func failTimes(failures int) func(int) error {
	calls := 0
	return func(int) error {
		calls++
		if calls <= failures {
			return errBoom
		}
		return nil
	}
}

// Semantic 1: n attempts produce exactly n-1 waits; no wait before the
// first attempt or after the final failure.
func TestWaitsAreAttemptsMinusOne(t *testing.T) {
	r := New(Policy{MaxAttempts: 3, Base: time.Millisecond}, nil, nil)
	r.sleep = func(time.Duration) {}

	calls := 0
	attempt, err := r.Do(func(int) error { calls++; return errBoom })
	if err == nil || calls != 3 || attempt != 3 {
		t.Fatalf("got calls=%d attempt=%d err=%v", calls, attempt, err)
	}
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("len(Delays())=%d, want 2", got)
	}
}

// Semantic 2: jitter-free sequence is Base*Factor^(k-1), capped.
func TestDeterministicBackoff(t *testing.T) {
	r := New(Policy{MaxAttempts: 5, Base: 100 * time.Millisecond, Factor: 2, Cap: 250 * time.Millisecond}, func(time.Duration) {}, nil)
	if _, err := r.Do(failTimes(5)); err == nil {
		t.Fatal("want error")
	}
	want := []time.Duration{100, 200, 250, 250}
	got := r.Delays()
	for i := range want {
		if got[i] != want[i]*time.Millisecond {
			t.Fatalf("delay[%d]=%v, want %v", i, got[i], want[i]*time.Millisecond)
		}
	}

	// Factor<=1 means constant Base.
	flat := New(Policy{MaxAttempts: 4, Base: 50 * time.Millisecond, Factor: 1}, func(time.Duration) {}, nil)
	flat.Do(failTimes(4))
	for i, d := range flat.Delays() {
		if d != 50*time.Millisecond {
			t.Fatalf("flat delay[%d]=%v, want 50ms", i, d)
		}
	}
}

// Semantic 3: jitter is bounded and consumes rnd exactly once per wait.
func TestJitterBoundsAndSingleDraw(t *testing.T) {
	seq := []float64{0, 0.5, 0.999}
	draws := 0
	rnd := func() float64 { v := seq[draws]; draws++; return v }

	p := Policy{MaxAttempts: 4, Base: 200 * time.Millisecond, Factor: 1, JitterPct: 50}
	r := New(p, func(time.Duration) {}, rnd)
	r.Do(failTimes(4))

	if draws != 3 {
		t.Fatalf("rnd called %d times, want 3 (once per wait)", draws)
	}
	got := r.Delays()
	lo, hi := 100*time.Millisecond, 300*time.Millisecond
	if got[0] != lo {
		t.Fatalf("r=0: got %v, want lower bound %v", got[0], lo)
	}
	if got[1] != 200*time.Millisecond {
		t.Fatalf("r=0.5: got %v, want midpoint 200ms", got[1])
	}
	if got[2] <= 299*time.Millisecond || got[2] > hi {
		t.Fatalf("r=0.999: got %v, want just under %v", got[2], hi)
	}
}

// Semantic 4: permanent errors abort immediately, no wait, no retry.
func TestPermanentAborts(t *testing.T) {
	r := New(Policy{MaxAttempts: 5, Base: time.Second}, func(time.Duration) {}, nil)
	calls := 0
	attempt, err := r.Do(func(int) error { calls++; return Permanent(errBoom) })
	if calls != 1 || attempt != 1 {
		t.Fatalf("calls=%d attempt=%d, want 1/1", calls, attempt)
	}
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err %v not ErrAborted", err)
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("err %v does not unwrap to errBoom", err)
	}
	if got := len(r.Delays()); got != 0 {
		t.Fatalf("waited %d times after permanent error", got)
	}
}

// Semantic 5: success on attempt k stops everything.
func TestSuccessStops(t *testing.T) {
	r := New(Policy{MaxAttempts: 5, Base: time.Millisecond}, func(time.Duration) {}, nil)
	calls := 0
	attempt, err := r.Do(func(int) error {
		calls++
		if calls < 3 {
			return errBoom
		}
		return nil
	})
	if err != nil || attempt != 3 || calls != 3 {
		t.Fatalf("attempt=%d calls=%d err=%v", attempt, calls, err)
	}
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("len(Delays())=%d, want 2", got)
	}
}

// Semantic 6: exhaustion wraps ErrExhausted and the LAST error.
func TestExhaustedWrapsLastError(t *testing.T) {
	errs := []error{errors.New("first"), errors.New("middle"), errors.New("last")}
	i := 0
	r := New(Policy{MaxAttempts: 3}, func(time.Duration) {}, nil)
	_, err := r.Do(func(int) error { e := errs[i]; i++; return e })
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("err %v not ErrExhausted", err)
	}
	if !errors.Is(err, errs[2]) {
		t.Fatalf("err %v does not unwrap to last error", err)
	}
	if errors.Is(err, errs[0]) {
		t.Fatalf("err %v unexpectedly matches first error", err)
	}
}

// Semantic 7: attempt numbers are 1..n, in order; MaxAttempts<=0 runs once.
func TestAttemptNumbering(t *testing.T) {
	var seen []int
	r := New(Policy{MaxAttempts: 4}, func(time.Duration) {}, nil)
	r.Do(func(a int) error { seen = append(seen, a); return errBoom })
	for i, a := range seen {
		if a != i+1 {
			t.Fatalf("attempts=%v, want 1..4", seen)
		}
	}

	once := New(Policy{MaxAttempts: 0}, func(time.Duration) {}, nil)
	calls := 0
	attempt, _ := once.Do(func(a int) error { calls++; return errBoom })
	if calls != 1 || attempt != 1 {
		t.Fatalf("MaxAttempts=0: calls=%d attempt=%d, want 1/1", calls, attempt)
	}
}
