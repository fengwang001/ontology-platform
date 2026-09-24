// Package budget counts deterministic match steps and enforces an upper bound.
// A *Counter is owned by a single matching run, so concurrent matches never
// share state.
package budget

import (
	"errors"
	"fmt"
)

// ErrBudget is returned when a match exceeds its step limit.
var ErrBudget = errors.New("match step budget exceeded")

// LimitError carries the limit that was crossed.
type LimitError struct {
	Limit int
}

// Error implements error.
func (e *LimitError) Error() string {
	return fmt.Sprintf("%s: limit=%d", ErrBudget, e.Limit)
}

// Unwrap enables errors.Is(err, ErrBudget).
func (e *LimitError) Unwrap() error { return ErrBudget }

// Counter records steps against an optional limit (<=0 means unlimited).
type Counter struct {
	steps int
	limit int
}

// New returns a Counter with the given step limit.
func New(limit int) *Counter { return &Counter{limit: limit} }

// Tick records one step. It is a no-op on a nil receiver. After the limit is
// reached, subsequent ticks keep returning the same error.
func (c *Counter) Tick() error {
	if c == nil {
		return nil
	}
	c.steps++
	if c.limit > 0 && c.steps > c.limit {
		return &LimitError{Limit: c.limit}
	}
	return nil
}

// Steps returns the number of recorded steps.
func (c *Counter) Steps() int {
	if c == nil {
		return 0
	}
	return c.steps
}
