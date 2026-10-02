// Package healthcheck implements an active health-check state machine with
// flap damping and a global fast-probe quota.
package healthcheck

import (
	"errors"
	"sync"
)

const (
	maxR        = 1000
	maxInterval = 1_000_000_000
	maxNow      = 1_000_000_000_000_000
)

var (
	ErrInvalidConfig    = errors.New("healthcheck: invalid config")
	ErrTargetOutOfRange = errors.New("healthcheck: target out of range")
	ErrInvalidTime      = errors.New("healthcheck: invalid time")
	ErrClockRegression  = errors.New("healthcheck: global clock regression")
	ErrProbeTooEarly    = errors.New("healthcheck: probe too early")
)

// Config holds the construction parameters of a Checker.
type Config struct {
	N                int   // number of targets, ids 0..N-1
	R                int64 // consecutive successes to recover (1..1000)
	F                int64 // consecutive failures to go unhealthy (>=1)
	I                int64 // normal interval
	FI               int64 // fast interval
	DI               int64 // unhealthy interval
	InitiallyHealthy bool
	Wf               int64 // flap window (1..1e9)
	Q                int   // global fast-probe quota (0..N)
}

// Snapshot is a copy of one target's observable state.
type Snapshot struct {
	Healthy bool
	A       int64   // consecutive successes
	B       int64   // consecutive failures
	Nd      int64   // next allowed probe time
	Fu      int64   // fast-quota lease expiry
	Tr      []int64 // transition times
}

type target struct {
	healthy bool
	a       int64
	b       int64
	nd      int64
	tr      []int64
	fu      int64
}

// Checker is a concurrency-safe health-check state machine.
type Checker struct {
	mu      sync.Mutex
	cfg     Config
	targets []target
	maxNow  int64
}

// New validates cfg and returns a Checker; any invalid field rejects the
// whole config with ErrInvalidConfig.
func New(cfg Config) (*Checker, error) {
	if cfg.N < 1 || cfg.R < 1 || cfg.R > maxR || cfg.F < 1 ||
		cfg.I < 1 || cfg.I > maxInterval ||
		cfg.FI < 1 || cfg.FI > maxInterval ||
		cfg.DI < 1 || cfg.DI > maxInterval ||
		cfg.Wf < 1 || cfg.Wf > maxInterval ||
		cfg.Q < 0 || cfg.Q > cfg.N {
		return nil, ErrInvalidConfig
	}
	c := &Checker{cfg: cfg, targets: make([]target, cfg.N)}
	for i := range c.targets {
		c.targets[i].healthy = cfg.InitiallyHealthy
	}
	return c, nil
}

// Probe records one probe outcome for target at time now.
func (c *Checker) Probe(targetID int, ok bool, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if targetID < 0 || targetID >= c.cfg.N {
		return ErrTargetOutOfRange
	}
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < c.maxNow {
		return ErrClockRegression
	}
	t := &c.targets[targetID]
	if now < t.nd {
		return ErrProbeTooEarly
	}

	if t.healthy {
		if ok {
			t.b = 0
		} else {
			t.b++
			if t.b >= c.cfg.F {
				t.healthy = false
				t.a, t.b = 0, 0
				t.tr = append(t.tr, now)
			}
		}
	} else {
		if !ok {
			t.a = 0
		} else {
			t.a++
			if t.a >= c.reff(t, now) {
				t.healthy = true
				t.a, t.b = 0, 0
				t.tr = append(t.tr, now)
			}
		}
	}

	interval := c.pickInterval(t, now)
	t.nd = now + interval
	c.maxNow = now
	return nil
}

// reff returns the effective rise threshold at time now: R*(1+min(g,4)),
// where g is the number of transitions still valid at now (t+Wf > now).
func (c *Checker) reff(t *target, now int64) int64 {
	var g int64
	for _, ts := range t.tr {
		if ts+c.cfg.Wf > now {
			g++
		}
	}
	if g > 4 {
		g = 4
	}
	return c.cfg.R * (1 + g)
}

// pickInterval selects the probe interval from the post-processing state and
// the global fast quota, updates fu accordingly, and returns the interval.
func (c *Checker) pickInterval(t *target, now int64) int64 {
	fast := t.healthy && t.b > 0 || !t.healthy && t.a > 0
	if !fast {
		t.fu = 0
		if t.healthy {
			return c.cfg.I
		}
		return c.cfg.DI
	}
	x := 0
	for i := range c.targets {
		other := &c.targets[i]
		if other != t && other.fu > now {
			x++
		}
	}
	if x >= c.cfg.Q {
		t.fu = 0
		if t.healthy {
			return c.cfg.I
		}
		return c.cfg.DI
	}
	t.fu = now + c.cfg.FI
	return c.cfg.FI
}

// State returns a copy of the target's state.
func (c *Checker) State(targetID int) (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if targetID < 0 || targetID >= c.cfg.N {
		return Snapshot{}, ErrTargetOutOfRange
	}
	t := &c.targets[targetID]
	return Snapshot{
		Healthy: t.healthy,
		A:       t.a,
		B:       t.b,
		Nd:      t.nd,
		Fu:      t.fu,
		Tr:      append([]int64(nil), t.tr...),
	}, nil
}

// Healthy returns the ascending list of healthy target ids.
func (c *Checker) Healthy() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, 0, c.cfg.N)
	for i := range c.targets {
		if c.targets[i].healthy {
			out = append(out, i)
		}
	}
	return out
}
