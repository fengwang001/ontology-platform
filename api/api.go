// Package api is the external entry point for the EWMA subsystem. It wraps
// stream.Stream and exposes a SelfCheck over a fixed operation sequence.
// Dependency direction: api -> stream -> ema, never reversed.
package api

import (
	"errors"
	"fmt"

	"ontology/stream"
)

// Re-exported sentinels so callers have one import for every failure mode.
var (
	ErrInvalidAlpha    = stream.ErrInvalidAlpha
	ErrRetractEmpty    = stream.ErrRetractEmpty
	ErrRetractNotFound = stream.ErrRetractNotFound
)

// API wraps a stream.Stream with a self-check.
type API struct {
	s *stream.Stream
}

// New constructs an API instance, rejecting alpha outside (0,1].
func New(alpha float64) (*API, error) {
	s, err := stream.New(alpha)
	if err != nil {
		return nil, err
	}
	return &API{s: s}, nil
}

// Add feeds one event.
func (a *API) Add(v float64) { a.s.Add(v) }

// Retract removes the most recent surviving occurrence of v.
func (a *API) Retract(v float64) error { return a.s.Retract(v) }

// Value returns the current EWMA.
func (a *API) Value() float64 { return a.s.Value() }

// Initialized reports whether the sequence is non-empty.
func (a *API) Initialized() bool { return a.s.Initialized() }

// eightStep replays the eight built-in operations and returns the EWMA
// after each step, matching the table in NOTES.md.
var eightStep = []struct {
	op    string
	v     float64
	isAdd bool
	want  float64
}{
	{"add", 10, true, 10},
	{"add", 20, true, 12.5},
	{"add", 10, true, 11.875},
	{"add", 30, true, 16.40625},
	{"retract", 10, false, 16.875},
	{"add", 40, true, 22.65625},
	{"retract", 20, false, 21.25},
	{"add", 50, true, 28.4375},
}

// recompute is the independent batch oracle: first value seeds, rest decay.
func recompute(hist []float64, alpha float64) (float64, bool) {
	if len(hist) == 0 {
		return 0, false
	}
	m := hist[0]
	for _, x := range hist[1:] {
		m = alpha*x + (1-alpha)*m
	}
	return m, true
}

// SelfCheck verifies all four invariants over the built-in sequence plus
// the failure and complexity probes. It returns nil iff every check passes.
func (a *API) SelfCheck() error {
	const alpha = 0.25
	// Fresh local instance: SelfCheck must not mutate the receiver.
	c, err := New(alpha)
	if err != nil {
		return err
	}
	var hist []float64
	for i, st := range eightStep {
		if st.isAdd {
			c.Add(st.v)
			hist = append(hist, st.v)
		} else {
			if err := c.Retract(st.v); err != nil {
				return fmt.Errorf("step %d: %w", i+1, err)
			}
			for j := len(hist) - 1; j >= 0; j-- {
				if hist[j] == st.v {
					hist = append(hist[:j], hist[j+1:]...)
					break
				}
			}
		}
		if got := c.Value(); got != st.want {
			return fmt.Errorf("step %d: EWMA=%v want %v", i+1, got, st.want)
		}
		// Invariant 1: value equals an independent batch recomputation.
		if want, ok := recompute(hist, alpha); !ok || c.Value() != want {
			return fmt.Errorf("step %d: diverges from batch recompute", i+1)
		}
		if !c.Initialized() {
			return fmt.Errorf("step %d: expected initialized", i+1)
		}
	}
	// Invariant 2: empty is undefined; first Add seeds without zero bias.
	e, _ := New(alpha)
	if e.Initialized() {
		return errors.New("empty sequence reports initialized")
	}
	e.Add(10)
	if e.Value() != 10 {
		return fmt.Errorf("first value seeded as %v want 10", e.Value())
	}
	// Invariant 4: the three failures are distinct and leave no trace.
	bad, err := New(0)
	if !errors.Is(err, ErrInvalidAlpha) || bad != nil {
		return errors.New("alpha=0 not rejected distinctly")
	}
	emp, _ := New(alpha)
	if err := emp.Retract(1); !errors.Is(err, ErrRetractEmpty) {
		return errors.New("empty retract not ErrRetractEmpty")
	}
	emp.Add(7)
	if err := emp.Retract(9); !errors.Is(err, ErrRetractNotFound) {
		return errors.New("missing retract not ErrRetractNotFound")
	}
	if emp.Value() != 7 || !emp.Initialized() {
		return errors.New("rejected retract mutated state")
	}
	emp.Add(3)
	if emp.Value() != 0.25*3+0.75*7 {
		return errors.New("instance unusable after rejected ops")
	}
	// Constant-time Add probe (counter value never exposed).
	if err := stream.VerifyAddComplexity(); err != nil {
		return err
	}
	return nil
}
