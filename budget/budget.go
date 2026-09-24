// Package budget counts matching steps and enforces a step cap.
package budget

import "errors"

// ErrExhausted is returned by matchers when a Meter's limit is exceeded.
var ErrExhausted = errors.New("step budget exhausted")

// Meter counts steps. The counter itself is unexported; callers
// observe it only through Steps.
type Meter struct {
	steps int64
	limit int64 // <= 0 means no limit
}

// New returns a Meter with the given step limit (<= 0 for unlimited).
func New(limit int64) *Meter { return &Meter{limit: limit} }

// Step records one unit of work and reports whether the meter is
// still within its limit.
func (m *Meter) Step() bool {
	m.steps++
	return m.limit <= 0 || m.steps <= m.limit
}

// Steps returns the number of steps recorded so far.
func (m *Meter) Steps() int64 { return m.steps }

// Limit returns the configured limit (<= 0 means unlimited).
func (m *Meter) Limit() int64 { return m.limit }
