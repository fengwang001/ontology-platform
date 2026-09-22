// Package budget keeps a byte-usage ledger with a hard upper limit.
package budget

import "fmt"

// ErrExceeded is returned when a reservation would push usage over
// the configured limit.
var ErrExceeded = fmt.Errorf("budget: byte limit exceeded")

// Budget tracks used bytes against a fixed limit. It is not safe for
// concurrent use; callers (reasm) serialize access.
type Budget struct {
	limit int
	used  int
}

// New creates a Budget allowing up to limit bytes in flight.
func New(limit int) *Budget {
	return &Budget{limit: limit}
}

// Reserve charges n bytes. If the reservation would exceed the limit
// it fails with ErrExceeded and leaves usage unchanged.
func (b *Budget) Reserve(n int) error {
	if n < 0 {
		panic("budget: negative reserve")
	}
	if b.used+n > b.limit {
		return ErrExceeded
	}
	b.used += n
	return nil
}

// Release returns n bytes to the budget.
func (b *Budget) Release(n int) {
	if n < 0 || n > b.used {
		panic("budget: release out of range")
	}
	b.used -= n
}

// Used returns the currently reserved byte count.
func (b *Budget) Used() int { return b.used }

// Limit returns the configured upper bound.
func (b *Budget) Limit() int { return b.limit }
