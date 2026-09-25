// Package api is the public entry point: a per-key FIFO reorder buffer with
// backpressure. It depends only on the dispatch package.
package api

import (
	"ontology/dispatch"
)

// Classifiable sentinel errors; the three failure modes are always distinct.
var (
	ErrEmptyKey     = dispatch.ErrEmptyKey
	ErrInvalidMax   = dispatch.ErrInvalidMax
	ErrBackpressure = dispatch.ErrBackpressure
)

// Buffer accepts (key, seq) events and guarantees per-key emission in
// strictly increasing, gap-free seq order, bounded by maxInFlight.
type Buffer struct {
	d *dispatch.Dispatcher
}

// New creates a Buffer with the given per-key in-flight bound.
func New(maxInFlight int) (*Buffer, error) {
	d, err := dispatch.New(maxInFlight)
	if err != nil {
		return nil, err
	}
	return &Buffer{d: d}, nil
}

// Feed delivers one event. It returns the seqs newly emitted for the key by
// this call (a consecutive run, possibly empty on buffer/duplicate).
func (b *Buffer) Feed(key string, seq int64) ([]int64, error) {
	return b.d.Feed(key, seq)
}

// Emitted returns a copy of the key's gap-free emitted prefix.
func (b *Buffer) Emitted(key string) []int64 { return b.d.Emitted(key) }

// Buffered returns the key's current in-flight (buffered) count.
func (b *Buffer) Buffered(key string) int { return b.d.Buffered(key) }

// Dropped returns the total duplicate drops across all keys.
func (b *Buffer) Dropped() int64 { return b.d.Dropped() }

// SelfCheck runs built-in event sequences through only the public API and
// verifies all four invariants, returning a classifiable error on failure.
func (b *Buffer) SelfCheck() error { return b.d.SelfCheck() }
