// Package policy defines retry and compensation strategies. All time
// flows through an injected Clock; nothing here reads the wall clock.
package policy

import (
	"sync"
	"time"

	"ontology/step"
)

// Clock is the only time source the orchestrator may use.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

// FakeClock is a manually advanced clock for tests and demos. Sleep
// never blocks; it only moves virtual time forward.
type FakeClock struct {
	mu    sync.Mutex
	now   time.Time
	slept time.Duration
}

// NewFakeClock starts a fake clock at base.
func NewFakeClock(base time.Time) *FakeClock {
	return &FakeClock{now: base}
}

// Now returns the virtual time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep advances virtual time without real waiting.
func (c *FakeClock) Sleep(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.slept += d
}

// Slept returns the total virtual time slept.
func (c *FakeClock) Slept() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slept
}

// Policy controls retries of step execution and of compensation.
//
// MaxRetries is a left-closed, right-open bound: with MaxRetries = N a
// step is executed at most N+1 times (attempts 1..N+1); the (N+1)-th
// failure is terminal.
type Policy struct {
	MaxRetries int
	// Backoff returns the wait before the next attempt; attempt is the
	// 1-based number of the attempt that just failed. Nil means no wait.
	Backoff func(attempt int) time.Duration
	// Retryable classifies errors; nil means every error is retryable.
	Retryable func(err error) bool
	// CompensateRetries bounds compensation retries the same way
	// (0 means compensation is tried exactly once).
	CompensateRetries int
}

// AllowRetry reports whether a step that just failed may be retried,
// based on the machine's journal-derived execution count.
func (p Policy) AllowRetry(m *step.Machine, err error) bool {
	if p.Retryable != nil && !p.Retryable(err) {
		return false
	}
	exec, _ := m.Counts()
	return exec <= p.MaxRetries
}

// AllowCompensateRetry reports whether a failed compensation may be
// retried; compAttempts is the number of attempts already made.
func (p Policy) AllowCompensateRetry(compAttempts int) bool {
	return compAttempts <= p.CompensateRetries
}

// Wait returns the backoff duration for the given failed attempt.
func (p Policy) Wait(attempt int) time.Duration {
	if p.Backoff == nil {
		return 0
	}
	return p.Backoff(attempt)
}
