// Package buf holds a single transaction change, the in-memory buffer, and the
// threshold rule for splitting the buffer into spillable chunks. It depends on
// no other package.
package buf

// Op is a change operation.
type Op int

const (
	// Set writes Key -> Val (overwrite).
	Set Op = iota + 1
	// Del removes Key (the key must not exist afterwards).
	Del
)

// Change is one mutation {Op, Key, Val}. Del carries Val == "".
type Change struct {
	Op       Op
	Key, Val string
}

// Buffer keeps at most memLimit pending changes. Appending past the limit
// returns the full block (the first memLimit changes, in append order) and the
// newest change becomes the sole occupant of the cleared buffer.
type Buffer struct {
	memLimit int
	pending  []Change
}

// NewBuffer creates a buffer with the given positive limit.
func NewBuffer(memLimit int) *Buffer {
	return &Buffer{memLimit: memLimit, pending: make([]Change, 0, memLimit)}
}

// Append appends c. When the buffer would hold more than memLimit changes, the
// full block of memLimit is returned (FIFO, every change kept, including Del)
// and the buffer restarts with c alone; otherwise it returns nil.
func (b *Buffer) Append(c Change) (spilled []Change) {
	b.pending = append(b.pending, c)
	if len(b.pending) > b.memLimit {
		spilled = append([]Change(nil), b.pending[:b.memLimit]...)
		b.pending = append(b.pending[:0], b.pending[b.memLimit:]...)
	}
	return spilled
}

// Pending returns a copy of the not-yet-spilled tail buffer, in append order.
func (b *Buffer) Pending() []Change {
	out := make([]Change, len(b.pending))
	copy(out, b.pending)
	return out
}

// Len is the number of pending changes.
func (b *Buffer) Len() int { return len(b.pending) }
