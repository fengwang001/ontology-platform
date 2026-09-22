// Package txid owns transaction identifiers: allocation, comparison and
// validity. All numbers come from an injected Source; this package never
// consults the wall clock or any global counter of its own.
package txid

import "errors"

// ID is a monotonically allocated, never-wrapping transaction number.
// The zero value is invalid: every real transaction gets a positive ID.
type ID uint64

// Invalid is the zero value, guaranteed never to be handed out by a Source.
const Invalid ID = 0

// Valid reports whether id was produced by a Source.
func (id ID) Valid() bool { return id != Invalid }

// Less reports whether id precedes other in allocation order.
func (id ID) Less(other ID) bool { return id < other }

// Source allocates transaction identifiers.
//
// Implementations must satisfy:
//   - Allocate returns strictly increasing values (id2 > id1), so the stream
//     is monotonic and can never wrap within a process lifetime;
//   - Allocate never returns Invalid;
//   - Peek returns the value the next Allocate would return without consuming
//     it, so callers can describe a "snapshot point" boundary;
//   - Exhausted is returned once no more IDs can be given out.
//
// The interface exists so tests and demos can inject deterministic or
// shared sources. All implementations must be safe for concurrent use.
type Source interface {
	Allocate() (ID, error)
	Peek() (ID, error)
}

// ErrExhausted is returned by a Source when no further IDs can be allocated.
var ErrExhausted = errors.New("txid: source exhausted")

// NewSource returns the default in-process monotonic source.
func NewSource() Source { return &memSource{} }

// NewSourceAt returns an in-process source whose next allocation is first.
// It is mainly useful for deterministic tests; first must be non-zero.
func NewSourceAt(first ID) Source {
	if first == Invalid {
		first = 1
	}
	return &memSource{next: first}
}

// NewFuncSource builds a Source from injected functions, allowing tests to
// drive allocation from an external, possibly scripted, source.
func NewFuncSource(allocate func() (ID, error), peek func() (ID, error)) Source {
	return &funcSource{allocate: allocate, peek: peek}
}
