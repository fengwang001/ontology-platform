// Package txid allocates and compares transaction IDs.
//
// IDs are monotonically increasing, never wrap, and the zero value is
// invalid. An Allocator is not safe for concurrent use; callers must
// serialize access (snapshot.Manager does so).
package txid

// ID is a transaction identifier. The zero value is invalid.
type ID uint64

// Valid reports whether id is a non-zero, allocated identifier.
func (id ID) Valid() bool { return id != 0 }

// Less reports whether id was allocated before other.
func (id ID) Less(other ID) bool { return id < other }

// Allocator hands out monotonically increasing IDs.
type Allocator struct{ next ID }

// NewAllocator returns an allocator whose first allocated ID is 1.
func NewAllocator() *Allocator { return &Allocator{next: 1} }

// Next allocates and returns a new ID, greater than every previous one.
func (a *Allocator) Next() ID {
	id := a.next
	a.next++
	return id
}

// Curr returns the ID that Next would allocate, without consuming it.
// Snapshots use Curr as their snapshot point.
func (a *Allocator) Curr() ID { return a.next }
