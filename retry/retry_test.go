package retry

import (
	"errors"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func failAlways(error) func(int) error {
	return func(int) error { return errBoom }
}

// Semantic 1: waits = attempts - 1; no wait before the first attempt and
// never after the final failure.
func TestDelaysOneFewerThanAttempts(t *testing.T) {
	r := New(Policy{MaxAttempts: 3, Base: time.Second}, func(time.Duration) {}, nil)
	n, err := r.Do(failAlways(nil))
	if n != 3 || !errors.Is(err, ErrExhausted) {
		t.Fatalf("got (%d, %v)", n, err)
	}
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("len(Delays()) = %d, want 2", got)
	}
}

// Semantic 2: deterministic backoff Base*Factor^(k-1), bounded by Cap.
func TestDeterministicBackoff(t *testing.T) {
	r := New(Policy{MaxAttempts: 5, Base: 100, Factor: 3}, func(time.Duration) {}, nil)
	r.Do(failAlways(nil))
	want := []time.Duration{100, 300, 900, 2700}
	got := r.Delays()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delay[%d] = %v, want %v (full %v)", i, got[i], want[i], got)
		}
	}
}

func TestBackoffCapAndNoGrowth(t *testing.T) {
	capped := New(Policy{MaxAttempts: 4, Base: 100, Factor: 10, Cap: 250}, func(time.Duration) {}, nil)
	capped.Do(failAlways(nil))
	for i, d := range capped.Delays() {
		want := []time.Duration{100, 250, 250}[i]
		if d != want {
			t.Fatalf("capped delay[%d] = %v, want %v", i, d, want)
		}
	}
	flat := New(Policy{MaxAttempts: 3, Base: 100, Factor: 1}, func(time.Duration) {}, nil)
	flat.Do(failAlways(nil))
	for i, d := range flat.Delays() {
		if d != 100 {
			t.Fatalf("flat delay[%d] = %v, want 100", i, d)
		}
	}
}

// Semantic 3: jitter is bounded and consumes rnd exactly once per wait.
func TestJitterBoundsAndSingleDraw(t *testing.T) {
	seq := []float64{0, 0.5, 0.999}
	calls := 0
	rnd := func() float64 {
		v := seq[calls]
		calls++
		return v
	}
	p := Policy{MaxAttempts: 4, Base: 1000, Factor: 1, JitterPct: 20}
	r := New(p, func(time.Duration) {}, rnd)
	r.Do(failAlways(nil))
	if calls != 3 {
		t.Fatalf("rnd called %d times, want 3 (once per wait)", calls)
	}
	got := r.Delays()
	const d = 1000.0
	lo, hi := d*0.8, d*1.2
	wantApprox := []float64{lo, d, d * 1.198}
	for i, w := range wantApprox {
		if got[i] < time.Duration(lo) || got[i] > time.Duration(hi) {
			t.Fatalf("delay[%d] = %v outside [%v, %v]", i, got[i], lo, hi)
		}
		if diff := float64(got[i]) - w; diff < -1 || diff > 1 {
			t.Fatalf("delay[%d] = %v, want ~%v", i, got[i], w)
		}
	}
}

// Semantic 4: permanent errors abort immediately, no wait, no retry.
func TestPermanentAborts(t *testing.T) {
	calls := 0
	slept := 0
	r := New(Policy{MaxAttempts: 5, Base: time.Second}, func(time.Duration) { slept++ }, nil)
	n, err := r.Do(func(int) error {
		calls++
		return Permanent(errBoom)
	})
	if calls != 1 || slept != 0 || n != 1 {
		t.Fatalf("calls=%d slept=%d n=%d", calls, slept, n)
	}
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err %v is not ErrAborted", err)
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("err %v does not wrap original", err)
	}
}

// Semantic 5: success stops immediately with the right attempt number.
func TestSuccessStops(t *testing.T) {
	calls := 0
	r := New(Policy{MaxAttempts: 5, Base: time.Second}, func(time.Duration) {}, nil)
	n, err := r.Do(func(int) error {
		calls++
		if calls == 3 {
			return nil
		}
		return errBoom
	})
	if err != nil || n != 3 || calls != 3 {
		t.Fatalf("n=%d calls=%d err=%v", n, calls, err)
	}
	if got := len(r.Delays()); got != 2 {
		t.Fatalf("len(Delays()) = %d, want 2", got)
	}
}

// Semantic 6: exhaustion exposes ErrExhausted and the LAST error.
func TestExhaustedWrapsLastError(t *testing.T) {
	errFirst := errors.New("first")
	errLast := errors.New("last")
	r := New(Policy{MaxAttempts: 3}, func(time.Duration) {}, nil)
	_, err := r.Do(func(attempt int) error {
		if attempt == 1 {
			return errFirst
		}
		return errLast
	})
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("err %v is not ErrExhausted", err)
	}
	if !errors.Is(err, errLast) {
		t.Fatalf("err %v does not wrap last error", err)
	}
	if errors.Is(err, errFirst) {
		t.Fatalf("err %v must not wrap the first error", err)
	}
}

// Semantic 7: attempt numbers are 1-based, consecutive; MaxAttempts<=0 runs once.
func TestAttemptNumbering(t *testing.T) {
	var seen []int
	r := New(Policy{MaxAttempts: 4}, func(time.Duration) {}, nil)
	r.Do(func(attempt int) error {
		seen = append(seen, attempt)
		return errBoom
	})
	for i, a := range seen {
		if a != i+1 {
			t.Fatalf("attempts = %v, want 1..4 consecutive", seen)
		}
	}
	once := New(Policy{MaxAttempts: 0}, func(time.Duration) {}, nil)
	runs := 0
	n, err := once.Do(func(int) error { runs++; return errBoom })
	if runs != 1 || n != 1 || !errors.Is(err, ErrExhausted) {
		t.Fatalf("runs=%d n=%d err=%v", runs, n, err)
	}
	if got := len(once.Delays()); got != 0 {
		t.Fatalf("len(Delays()) = %d, want 0", got)
	}
}
