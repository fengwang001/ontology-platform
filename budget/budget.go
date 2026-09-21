// Package budget tracks a hard byte budget for in-flight reassembly work.
//
// A Budget is a plain ledger: callers acquire bytes before storing them and
// release them when the bytes are discarded or delivered. It is not safe for
// concurrent use; callers must synchronize externally.
package budget

import "errors"

// ErrExhausted is returned when an acquisition would push usage over the limit.
var ErrExhausted = errors.New("budget: byte limit exceeded")

// Budget is a byte ledger with a hard upper limit.
type Budget struct {
	limit int64
	used  int64
}

// New returns a Budget that allows at most limit bytes to be held at once.
func New(limit int64) *Budget {
	return &Budget{limit: limit}
}

// TryAcquire reserves n bytes. If the reservation would exceed the limit it
// returns ErrExhausted and the ledger is left unchanged.
func (b *Budget) TryAcquire(n int64) error {
	if n < 0 {
		panic("budget: negative acquire")
	}
	if b.used+n > b.limit {
		return ErrExhausted
	}
	b.used += n
	return nil
}

// Release returns n previously acquired bytes to the ledger.
func (b *Budget) Release(n int64) {
	if n < 0 {
		panic("budget: negative release")
	}
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
}

// Used reports the currently reserved byte count.
func (b *Budget) Used() int64 {
	return b.used
}

// Limit reports the configured upper bound.
func (b *Budget) Limit() int64 {
	return b.limit
}
