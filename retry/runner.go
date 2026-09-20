package retry

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// Runner executes functions according to a retry Policy. It is safe for
// concurrent use; concurrent Do calls are serialized so that each run's
// recorded delays never interleave.
type Runner struct {
	policy Policy
	sleep  func(time.Duration)
	rnd    func() float64

	mu     sync.Mutex
	delays []time.Duration
}

// New builds a Runner. sleep waits for the given duration (injectable for
// tests); rnd returns a random value in [0, 1) used for jitter. Nil values
// fall back to time.Sleep and a default random source.
func New(p Policy, sleep func(time.Duration), rnd func() float64) *Runner {
	if sleep == nil {
		sleep = time.Sleep
	}
	if rnd == nil {
		rnd = rand.Float64
	}
	return &Runner{
		policy: p.normalized(),
		sleep:  sleep,
		rnd:    rnd,
	}
}

// Do runs fn until it succeeds, returns a permanent error, or the attempt
// limit is reached. It returns the 1-based attempt number of the final
// call and the terminal error (nil on success).
//
// The first attempt runs immediately; a wait happens only between two
// attempts, never after the last failure.
func (r *Runner) Do(fn func(attempt int) error) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.delays = r.delays[:0]
	max := r.policy.MaxAttempts
	var lastErr error
	for attempt := 1; attempt <= max; attempt++ {
		err := fn(attempt)
		if err == nil {
			return attempt, nil
		}
		if cause, permanent := asPermanent(err); permanent {
			return attempt, fmt.Errorf("%w: %w", ErrAborted, cause)
		}
		lastErr = err
		if attempt == max {
			break
		}
		d := r.policy.delay(attempt, r.rnd)
		r.delays = append(r.delays, d)
		r.sleep(d)
	}
	return max, fmt.Errorf("%w: %w", ErrExhausted, lastErr)
}

// Delays returns the wait intervals of the most recent Do call, in the
// order they occurred.
func (r *Runner) Delays() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Duration(nil), r.delays...)
}
