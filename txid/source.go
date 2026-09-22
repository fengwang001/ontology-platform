package txid

import (
	"errors"
	"sync"
)

// ErrExhausted is returned when the 64-bit transaction identifier space is
// used up. A correct allocator refuses to wrap around because wrapping would
// let an old identifier be mistaken for a fresh, visible transaction.
var ErrExhausted = errors.New("txid: identifier space exhausted")

// Source mints transaction identifiers. Implementations must hand out values
// that are strictly greater than every previously minted value.
type Source interface {
	// Next returns a fresh, never-before-returned identifier.
	Next() (TxID, error)
}

// Counter is a thread-safe Source backed by a counter.
//
// The starting point is injected (via the zero value the first minted
// identifier is 1); production code wires one shared Counter, tests inject
// deterministic ones. No wall clock is consulted.
type Counter struct {
	mu    sync.Mutex
	last  TxID
	first TxID
}

// NewCounter returns a Counter whose first minted identifier is start.
// A zero start means the first identifier will be 1.
func NewCounter(start TxID) *Counter {
	return &Counter{first: start}
}

// Next implements Source. It never returns a wrapped identifier.
func (c *Counter) Next() (TxID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == Zero {
		next := c.first
		if next == Zero {
			next = 1
		}
		c.last = next
		return next, nil
	}
	if c.last == ^TxID(0) {
		return Zero, ErrExhausted
	}
	c.last++
	return c.last, nil
}

// Last returns the most recently minted identifier, or Zero if none.
func (c *Counter) Last() TxID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}
