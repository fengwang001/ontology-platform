// Package budget keeps a byte-level ledger with a hard upper limit.
//
// A Budget tracks how many bytes are currently reserved. Reservations
// that would exceed the limit are rejected without changing any state.
// It has no dependencies on other packages and is not internally
// synchronized; callers must serialize access.
package budget

import (
	"errors"
	"fmt"
)

// ErrExhausted is returned when a reservation would exceed the limit.
var ErrExhausted = errors.New("budget: limit exceeded")

// Budget is a byte ledger with a hard upper limit.
type Budget struct {
	limit int64
	used  int64
}

// New returns a Budget that allows at most limit bytes in flight.
func New(limit int64) *Budget {
	return &Budget{limit: limit}
}

// Limit returns the configured upper bound in bytes.
func (b *Budget) Limit() int64 { return b.limit }

// Used returns the number of bytes currently reserved.
func (b *Budget) Used() int64 { return b.used }

// TryReserve reserves n bytes. If the reservation would exceed the
// limit, it returns an error wrapping ErrExhausted and leaves the
// ledger completely unchanged.
func (b *Budget) TryReserve(n int64) error {
	if n < 0 {
		return fmt.Errorf("budget: cannot reserve negative amount %d", n)
	}
	if b.used+n > b.limit {
		return fmt.Errorf("%w: need %d more, only %d of %d free",
			ErrExhausted, n, b.limit-b.used, b.limit)
	}
	b.used += n
	return nil
}

// Release returns n bytes to the ledger. Releasing more than is
// currently used is a programming error and panics.
func (b *Budget) Release(n int64) {
	if n < 0 || n > b.used {
		panic(fmt.Sprintf("budget: invalid release of %d with %d used", n, b.used))
	}
	b.used -= n
}
