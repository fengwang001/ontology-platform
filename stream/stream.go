// Package stream drives an event sequence over an ema.EWMA. It adds
// concurrency control: writers are exclusive while Value/Initialized allow
// concurrent readers. The dependency is one-way: stream -> ema.
package stream

import (
	"sync"

	"ontology/ema"
)

// Re-exported sentinels so upper layers depend only on stream.
var (
	ErrInvalidAlpha    = ema.ErrInvalidAlpha
	ErrRetractEmpty    = ema.ErrRetractEmpty
	ErrRetractNotFound = ema.ErrRetractNotFound
)

// VerifyAddComplexity delegates to the ema constant-time Add probe; it
// reports pass/fail only, never the underlying counter.
func VerifyAddComplexity() error { return ema.VerifyAddComplexity() }

// Stream is a concurrency-safe facade over ema.EWMA. Rejected operations
// are rejected inside ema before any state changes, so failure leaves no
// trace here as well.
type Stream struct {
	mu sync.RWMutex
	e  *ema.EWMA
}

// New constructs a Stream with the given decay factor.
func New(alpha float64) (*Stream, error) {
	e, err := ema.New(alpha)
	if err != nil {
		return nil, err
	}
	return &Stream{e: e}, nil
}

// Add feeds one event.
func (s *Stream) Add(v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.e.Add(v)
}

// Retract removes the most recent surviving occurrence of v.
func (s *Stream) Retract(v float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.e.Retract(v)
}

// Value returns the current EWMA.
func (s *Stream) Value() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.e.Value()
}

// Initialized reports whether the sequence is non-empty.
func (s *Stream) Initialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.e.Initialized()
}
