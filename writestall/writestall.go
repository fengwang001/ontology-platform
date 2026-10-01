// Package writestall implements an LSM write-stall controller with
// hysteresis. The controller migrates between Normal, Slowdown and
// Stopped based on the number of L0 files, pending compaction bytes
// and frozen memtables, and reports a slowdown delay.
//
// All methods are safe for concurrent use; the result of concurrent
// calls is equivalent to some serial order.
package writestall

import (
	"errors"
	"sync"
)

// State is the write-stall state of the controller.
type State int

const (
	StateNormal State = iota
	StateSlowdown
	StateStopped
)

func (s State) String() string {
	switch s {
	case StateNormal:
		return "Normal"
	case StateSlowdown:
		return "Slowdown"
	case StateStopped:
		return "Stopped"
	default:
		return "Unknown"
	}
}

// Distinguishable rejection reasons.
var (
	ErrSlowdownThresholdOrder = errors.New("writestall: S1 must be smaller than S2")
	ErrPendingThresholdOrder  = errors.New("writestall: P1 must be smaller than P2")
	ErrImmThreshold           = errors.New("writestall: I must be positive")
	ErrMaxDelay               = errors.New("writestall: D must be positive")
	ErrNegativeL0             = errors.New("writestall: n0 must not be negative")
	ErrNegativePending        = errors.New("writestall: pend must not be negative")
	ErrNegativeImm            = errors.New("writestall: imm must not be negative")
	ErrWriteStopped           = errors.New("writestall: writes are stopped")
)

// Controller is a thread-safe LSM write-stall controller with hysteresis.
type Controller struct {
	mu       sync.Mutex
	s1, s2   int64 // L0 file count slowdown / stop thresholds
	p1, p2   int64 // pending compaction bytes slowdown / stop thresholds
	immStop  int64 // frozen memtable stop threshold
	maxDelay int64 // maximum slowdown delay in microseconds

	state State
	delay int64 // delay produced by the most recent accepted Observe
}

// NewController validates the thresholds and returns a controller in
// StateNormal. Validation errors are checked in the order S, P, I, D
// and only the first one is reported.
func NewController(s1, s2, p1, p2, immStop, maxDelay int64) (*Controller, error) {
	if s1 >= s2 {
		return nil, ErrSlowdownThresholdOrder
	}
	if p1 >= p2 {
		return nil, ErrPendingThresholdOrder
	}
	if immStop <= 0 {
		return nil, ErrImmThreshold
	}
	if maxDelay <= 0 {
		return nil, ErrMaxDelay
	}
	return &Controller{
		s1: s1, s2: s2,
		p1: p1, p2: p2,
		immStop:  immStop,
		maxDelay: maxDelay,
		state:    StateNormal,
	}, nil
}

// recoveryLine returns floor(3T/4) for a positive threshold T.
func recoveryLine(t int64) int64 {
	return 3 * t / 4
}

// Observe feeds one observation and advances the state machine.
// Negative arguments are rejected in the order n0, pend, imm and only
// the first error is reported; a rejected call does not change state.
// On error the returned state and delay reflect the unchanged current
// state.
func (c *Controller) Observe(n0, pend, imm int64) (State, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n0 < 0 {
		return c.state, c.delay, ErrNegativeL0
	}
	if pend < 0 {
		return c.state, c.delay, ErrNegativePending
	}
	if imm < 0 {
		return c.state, c.delay, ErrNegativeImm
	}

	stop := n0 >= c.s2 || pend >= c.p2 || imm >= c.immStop
	slow := n0 >= c.s1 || pend >= c.p1
	switch {
	case stop:
		c.state = StateStopped
	case c.state == StateStopped:
		if n0 < recoveryLine(c.s2) && pend < recoveryLine(c.p2) && imm < recoveryLine(c.immStop) {
			if slow {
				c.state = StateSlowdown
			} else {
				c.state = StateNormal
			}
		}
	case slow:
		c.state = StateSlowdown
	case c.state == StateSlowdown:
		if n0 < recoveryLine(c.s1) && pend < recoveryLine(c.p1) {
			c.state = StateNormal
		}
	default:
		c.state = StateNormal
	}

	if c.state == StateSlowdown {
		c.delay = c.slowdownDelay(n0, pend)
	} else {
		c.delay = 0
	}
	return c.state, c.delay, nil
}

// slowdownDelay computes max(1, ceil(D*rho)) with integer rational
// arithmetic, where rho is the larger of (n0-S1)/(S2-S1) and
// (pend-P1)/(P2-P1) clamped to [0,1].
func (c *Controller) slowdownDelay(n0, pend int64) int64 {
	num, den := int64(0), int64(1)
	if v := n0 - c.s1; v > 0 {
		num, den = v, c.s2-c.s1
	}
	if v := pend - c.p1; v > 0 {
		if pd := c.p2 - c.p1; v*den > num*pd {
			num, den = v, pd
		}
	}
	if num <= 0 {
		return 1
	}
	if num > den {
		num = den // clamp rho to 1
	}
	delay := (c.maxDelay*num + den - 1) / den
	if delay < 1 {
		delay = 1
	}
	return delay
}

// Admit reports whether a write may proceed. It returns
// ErrWriteStopped while stopped; otherwise it returns the current
// delay in microseconds (0 in StateNormal).
func (c *Controller) Admit() (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateStopped {
		return 0, ErrWriteStopped
	}
	return c.delay, nil
}

// State returns the current state.
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Delay returns the delay produced by the most recent accepted
// Observe (0 unless the controller is in StateSlowdown).
func (c *Controller) Delay() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.delay
}
