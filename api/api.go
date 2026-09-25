// Package api is the public face of the EWMA-with-retraction
// service. It depends on stream (which depends on ema); nothing
// depends back.
package api

import (
	"errors"
	"fmt"

	"ontology/ema"
	"ontology/stream"
)

// Re-exported sentinels so callers can errors.Is without importing
// the ema package.
var (
	ErrInvalidAlpha = ema.ErrInvalidAlpha
	ErrNotFound     = ema.ErrNotFound
	ErrEmpty        = ema.ErrEmpty
)

// API is a concurrency-safe EWMA service instance.
type API struct {
	st *stream.Stream
}

// New validates alpha and returns a ready instance.
func New(alpha float64) (*API, error) {
	st, err := stream.New(alpha)
	if err != nil {
		return nil, err
	}
	return &API{st: st}, nil
}

// Add folds v into the EWMA.
func (a *API) Add(v float64) { a.st.Add(v) }

// Retract removes the most recent live v; errors leave state intact.
func (a *API) Retract(v float64) error { return a.st.Retract(v) }

// Value returns the current EWMA.
func (a *API) Value() float64 { return a.st.Value() }

// Initialized reports whether the EWMA is defined.
func (a *API) Initialized() bool { return a.st.Initialized() }

// SelfCheck replays built-in operation sequences on throwaway
// instances and verifies the four invariants. It never touches the
// receiver's state and is safe for concurrent use.
func (a *API) SelfCheck() error {
	// Invariant 1+3: random-ish Add/Retract sequence matches a
	// from-scratch batch recompute over the surviving history.
	s, err := New(0.25)
	if err != nil {
		return err
	}
	var hist []float64
	ops := []struct {
		add bool
		v   float64
	}{{true, 10}, {true, 20}, {true, 10}, {true, 30}, {false, 10},
		{true, 40}, {false, 20}, {true, 50}, {false, 10}, {false, 40}}
	for _, op := range ops {
		if op.add {
			s.Add(op.v)
			hist = append(hist, op.v)
		} else {
			if err := s.Retract(op.v); err != nil {
				return fmt.Errorf("selfcheck retract: %w", err)
			}
			for i := len(hist) - 1; i >= 0; i-- {
				if hist[i] == op.v {
					hist = append(hist[:i], hist[i+1:]...)
					break
				}
			}
		}
	}
	if got, want := s.Value(), batch(0.25, hist); got != want {
		return fmt.Errorf("selfcheck: value %v != batch %v", got, want)
	}
	// Invariant 2: empty is uninitialized; first Add seeds directly.
	fresh, _ := New(0.5)
	if fresh.Initialized() {
		return errors.New("selfcheck: empty sequence initialized")
	}
	fresh.Add(7)
	if fresh.Value() != 7 {
		return errors.New("selfcheck: zero-seed bias detected")
	}
	// Invariant 4: rejected ops leave no trace.
	before := s.Value()
	if err := s.Retract(999); !errors.Is(err, ErrNotFound) {
		return errors.New("selfcheck: missing ErrNotFound")
	}
	if s.Value() != before {
		return errors.New("selfcheck: failed retract mutated state")
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidAlpha) {
		return errors.New("selfcheck: missing ErrInvalidAlpha")
	}
	if _, err := New(1.5); !errors.Is(err, ErrInvalidAlpha) {
		return errors.New("selfcheck: alpha>1 accepted")
	}
	empty, _ := New(0.5)
	if err := empty.Retract(1); !errors.Is(err, ErrEmpty) {
		return errors.New("selfcheck: missing ErrEmpty")
	}
	return nil
}

// batch recomputes the EWMA over hist from scratch: first value is
// the seed, the rest apply ema = alpha*x + (1-alpha)*ema.
func batch(alpha float64, hist []float64) float64 {
	v := hist[0]
	for _, x := range hist[1:] {
		v = alpha*x + (1-alpha)*v
	}
	return v
}
