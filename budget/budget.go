// Package budget tracks the resident-memory allowance of the external
// sort pipeline. Charges are conservative byte estimates (see
// record.Record.Size); the hard limit may never be exceeded.
package budget

import (
	"errors"
	"sync"
)

var (
	// ErrRecordTooLarge is returned when a single record exceeds the
	// entire budget and therefore could never be resident.
	ErrRecordTooLarge = errors.New("budget: record larger than limit")
	// ErrInvalidLimit is returned for non-positive limits.
	ErrInvalidLimit = errors.New("budget: limit must be positive")
)

// Budget is a goroutine-safe allowance counter.
type Budget struct {
	mu     sync.Mutex
	cond   sync.Cond
	limit  uint64
	used   uint64
	peak   uint64
	closed bool
}

// New creates a Budget with the given hard limit in bytes.
func New(limit uint64) (*Budget, error) {
	if limit == 0 {
		return nil, ErrInvalidLimit
	}
	b := &Budget{limit: limit}
	b.cond.L = &b.mu
	return b, nil
}

// Limit returns the configured hard limit.
func (b *Budget) Limit() uint64 { return b.limit }

// Used returns the currently charged resident bytes.
func (b *Budget) Used() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Peak returns the high-water mark of Used.
func (b *Budget) Peak() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// ErrClosed is returned from Charge after Close.
var ErrClosed = errors.New("budget: closed")

// Close wakes all blocked ingesters; subsequent Charge calls fail with
// ErrClosed instead of blocking.
func (b *Budget) Close() {
	b.mu.Lock()
	b.closed = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

// Charge reserves n resident bytes. When the charge would cross the
// hard limit it blocks until releases make room, so the resident total
// can never exceed the limit. Records handed to an in-flight spill stay
// charged until the spill finishes and the slice is released; that is
// what keeps concurrent ingesters from overcommitting during a spill.
func (b *Budget) Charge(n uint64) error {
	if n > b.limit {
		return ErrRecordTooLarge
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used+n > b.limit && !b.closed {
		b.cond.Wait()
	}
	if b.closed {
		return ErrClosed
	}
	b.used += n
	if b.used > b.peak {
		b.peak = b.used
	}
	return nil
}

// TryCharge is the non-blocking form of Charge: it either reserves n
// bytes immediately or returns false (and never blocks).
func (b *Budget) TryCharge(n uint64) (bool, error) {
	if b.limit == 0 {
		return false, ErrInvalidLimit
	}
	if n > b.limit {
		return false, ErrRecordTooLarge
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false, ErrClosed
	}
	if b.used+n > b.limit {
		return false, nil
	}
	b.used += n
	if b.used > b.peak {
		b.peak = b.used
	}
	return true, nil
}

// Release un-charges n bytes, clamping at zero.
func (b *Budget) Release(n uint64) {
	b.mu.Lock()
	if n > b.used {
		n = b.used
	}
	b.used -= n
	b.cond.Broadcast()
	b.mu.Unlock()
}
