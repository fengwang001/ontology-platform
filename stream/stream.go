// Package stream drives an ema.EMA with a sequence of Add/Retract
// events and exposes concurrency-safe read access to the current
// EWMA state.
package stream

import (
	"sync"

	"ontology/ema"
)

// Stream serializes event application and reads with a RWMutex, so
// Value/Initialized/Count may be called from many goroutines.
type Stream struct {
	mu   sync.RWMutex
	core *ema.EMA
}

// New validates alpha (propagating ema.ErrInvalidAlpha) and returns
// an empty stream.
func New(alpha float64) (*Stream, error) {
	c, err := ema.New(alpha)
	if err != nil {
		return nil, err
	}
	return &Stream{core: c}, nil
}

// Add appends a value event.
func (s *Stream) Add(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.core.Add(v)
}

// Retract removes the most recent live occurrence of v. Any error
// (ema.ErrEmpty / ema.ErrNotFound) leaves the stream untouched.
func (s *Stream) Retract(v float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.core.Retract(v)
}

// Value returns the current EWMA.
func (s *Stream) Value() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.core.Value()
}

// Initialized reports whether the EWMA is defined.
func (s *Stream) Initialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.core.Initialized()
}

// Count returns the number of live values in the sequence.
func (s *Stream) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.core.Len()
}
