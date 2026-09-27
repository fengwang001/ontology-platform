// Package ws delays the decision on a run of spaces and tabs:
// the run is trailing only when a line ending (or, at stream end,
// the absence of one) is eventually seen. It has no dependencies.
package ws

// IsSpace reports whether b is a trailing-whitespace candidate.
// Only ' ' and '\t' qualify.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Decider buffers one undecided run of spaces/tabs.
//
// Usage per byte of line content:
//   - AddSpace for a space/tab (kept buffered);
//   - Commit before emitting a non-space byte (the run is interior);
//   - Drop when a line ending arrives (the run is trailing);
//   - Flush at end of stream (a dangling run is interior, since no
//     line ending follows it).
//
// A Decider is not safe for concurrent use.
type Decider struct {
	pending []byte
}

// New creates a Decider.
func New() *Decider { return &Decider{} }

// AddSpace buffers one space or tab.
func (d *Decider) AddSpace(b byte) { d.pending = append(d.pending, b) }

// PendingLen is the number of buffered, still-undecided bytes.
func (d *Decider) PendingLen() int { return len(d.pending) }

// Pending returns the buffered run without clearing it.
func (d *Decider) Pending() []byte { return d.pending }

// Commit returns the buffered run (it is interior whitespace) and clears it.
func (d *Decider) Commit() []byte {
	run := d.pending
	d.pending = nil
	return run
}

// Drop discards the buffered run (it is trailing whitespace).
func (d *Decider) Drop() { d.pending = nil }

// Flush ends the stream: a run not followed by a line ending is
// interior and must be committed.
func (d *Decider) Flush() []byte { return d.Commit() }
