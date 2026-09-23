// Package state implements the closed/open/half-open transition machine.
package state

import (
	"errors"

	"ontology/tally"
)

// State is one of the three breaker states.
type State int

const (
	// Closed sends calls through and watches the failure window.
	Closed State = iota
	// Open rejects every call until the cooldown elapses.
	Open
	// HalfOpen admits at most Quota concurrent probe calls.
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "closed"
	}
}

// Distinct, decidable configuration errors.
var (
	ErrBadQuota    = errors.New("breaker: quota must be positive")
	ErrBadCooldown = errors.New("breaker: cooldown must be positive")
	ErrBadWindow   = errors.New("breaker: window must be positive")
)

// Config configures the machine.
type Config struct {
	// Quota is the hard cap of concurrently in-flight half-open probes (>0).
	Quota int
	// CooldownMS is the open-state duration in milliseconds (>0).
	CooldownMS int64
	// Window is the closed-state sample capacity (>0).
	Window int
	// FailureRate in (0,1] opens the breaker when reached.
	FailureRate float64
	// MinSamples is the minimum window size allowed to trip (>=1).
	MinSamples int
}

// Validate returns a distinct sentinel error per invalid field.
func (c Config) Validate() error {
	switch {
	case c.Quota <= 0:
		return ErrBadQuota
	case c.CooldownMS <= 0:
		return ErrBadCooldown
	case c.Window <= 0:
		return ErrBadWindow
	case c.MinSamples < 1:
		return ErrBadWindow
	case c.FailureRate <= 0 || c.FailureRate > 1:
		return ErrBadWindow
	default:
		return nil
	}
}

// Stats is a consistent snapshot of the machine.
type Stats struct {
	State         State
	OpenAtMS      int64
	OpenUntilMS   int64
	WindowTotal   int
	WindowFailure int
	Requests      int
	Executed      int
	Rejected      int
	Inflight      int
}

// Machine owns all mutable state behind one mutex.
type Machine struct {
	cfg Config
	tal *tally.Tally

	st      State
	openAt  int64
	gen     uint64
	inflight int
	issued  int
	succ    int

	requests int
	executed int
	rejected int
}

// New validates the config and returns a ready closed machine.
func New(cfg Config) (*Machine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
}
	return &Machine{cfg: cfg, tal: tally.New(cfg.Window), st: Closed}, nil
}
