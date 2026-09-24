// Package chanbuf holds the per-channel state for barrier alignment:
// block flag, arrived-barrier count and a FIFO buffer. It depends on nothing.
package chanbuf

// Kind discriminates input elements.
type Kind int

const (
	Record  Kind = iota // carries Key/Val, applied to the sum state
	Barrier             // carries checkpoint ID
)

// Item is one input element (and, unchanged, one output element).
type Item struct {
	Ch   int
	Kind Kind
	Key  string
	Val  int64
	ID   int64
}

// Chan is the state of a single input channel.
type Chan struct {
	blocked  bool
	barriers int64 // barriers arrived so far (including buffered ones)
	buf      []Item
}

// Blocked reports whether the channel is blocked on a barrier.
func (c *Chan) Blocked() bool { return c.blocked }

// Barriers returns how many barriers have arrived on this channel.
func (c *Chan) Barriers() int64 { return c.barriers }

// Len returns the number of buffered elements.
func (c *Chan) Len() int { return len(c.buf) }

// Block marks the channel blocked.
func (c *Chan) Block() { c.blocked = true }

// Unblock clears the blocked flag.
func (c *Chan) Unblock() { c.blocked = false }

// NoteBarrier counts one arrived barrier.
func (c *Chan) NoteBarrier() { c.barriers++ }

// Enqueue appends an element to the FIFO buffer.
func (c *Chan) Enqueue(it Item) { c.buf = append(c.buf, it) }

// Dequeue removes and returns the oldest buffered element.
func (c *Chan) Dequeue() (Item, bool) {
	if len(c.buf) == 0 {
		return Item{}, false
	}
	it := c.buf[0]
	c.buf = c.buf[1:]
	return it, true
}

// Items returns a copy of the buffered elements in arrival order.
func (c *Chan) Items() []Item { return append([]Item(nil), c.buf...) }

// Clone returns an independent copy (buffer contents copied).
func (c *Chan) Clone() Chan {
	n := *c
	n.buf = append([]Item(nil), c.buf...)
	return n
}
