package retry

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Runner executes functions according to a retry Policy.
// It is safe for concurrent use and may be reused across Do calls.
type Runner struct {
	policy Policy
	sleep  func(time.Duration)
	rnd    func() float64

	mu     sync.Mutex
	delays []time.Duration // delays of the most recently finished Do
}

// New builds a Runner. sleep waits between attempts (nil means
// time.Sleep); rnd supplies jitter randomness in [0,1) (nil means
// a math/rand/v2 source).
func New(p Policy, sleep func(time.Duration), rnd func() float64) *Runner {
	if sleep == nil {
		sleep = time.Sleep
	}
	if rnd == nil {
		rnd = rand.Float64
	}
	return &Runner{policy: p, sleep: sleep, rnd: rnd}
}

// Do runs fn up to MaxAttempts times. fn receives the 1-based attempt
// number. It returns the attempt that succeeded and nil error, or the
// last attempt number and a wrapped ErrExhausted / ErrAborted.
//
// There is never a wait before the first attempt nor after the final
// failed attempt: n attempts produce exactly n-1 waits.
func (r *Runner) Do(fn func(attempt int) error) (int, error) {
	max := r.policy.attempts()
	delays := make([]time.Duration, 0, max-1)
	var lastErr error

	for attempt := 1; attempt <= max; attempt++ {
		err := fn(attempt)
		if err == nil {
			r.store(delays)
			return attempt, nil
		}
		if cause, ok := asPermanent(err); ok {
			r.store(delays)
			return attempt, fmt.Errorf("%w: %w", ErrAborted, cause)
		}
		lastErr = err
		if attempt == max {
			break // never wait after the final failure
		}
		d := r.policy.delay(attempt, r.rnd)
		delays = append(delays, d)
		r.sleep(d)
	}

	r.store(delays)
	return max, fmt.Errorf("%w: %w", ErrExhausted, lastErr)
}

// Delays returns the waits performed by the most recent Do call, in
// order. The result is a copy; mutating it does not affect the Runner.
func (r *Runner) Delays() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Duration, len(r.delays))
	copy(out, r.delays)
	return out
}

func (r *Runner) store(delays []time.Duration) {
	r.mu.Lock()
	r.delays = delays
	r.mu.Unlock()
}
