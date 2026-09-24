// Package align holds the per-input-channel state used while aligning
// checkpoint barriers: the blocking marker, the in-flight record buffer and
// the id of the barrier the channel is waiting on. It depends on no other
// package.
package align

// Channel is the alignment state of a single input channel.
type Channel struct {
	blockedID int   // barrier id blocking this channel; 0 means unblocked
	pending   []int // deltas that arrived while blocked, in arrival order
	lastID    int   // largest barrier id ever seen on this channel
}

// NewChannel returns an unblocked channel with an empty in-flight buffer.
func NewChannel() *Channel { return &Channel{} }

// Block marks the channel as blocked, waiting for alignment of id.
func (c *Channel) Block(id int) { c.blockedID = id }

// MarkBarrier records that a barrier with id was injected on this channel.
func (c *Channel) MarkBarrier(id int) { c.lastID = id }

// LastBarrier returns the largest barrier id seen on this channel.
func (c *Channel) LastBarrier() int { return c.lastID }

// Blocked reports whether the channel is held behind a barrier.
func (c *Channel) Blocked() bool { return c.blockedID > 0 }

// BarrierID returns the id the channel is blocked on, or 0 when unblocked.
func (c *Channel) BarrierID() int { return c.blockedID }

// Hold appends delta to the in-flight buffer, preserving arrival order.
func (c *Channel) Hold(delta int) { c.pending = append(c.pending, delta) }

// Pending returns the buffered deltas in arrival order.
func (c *Channel) Pending() []int { return c.pending }

// Reset clears the blocking marker and the in-flight buffer once alignment
// for the barrier has completed.
func (c *Channel) Reset() {
	c.blockedID = 0
	c.pending = c.pending[:0]
}
