// Package ws delays decisions about a run of trailing spaces and tabs.
package ws

// Event is one buffered space or tab.
type Event struct {
	Byte   byte
	Offset int
}

// IsSpace reports whether b is line-blank whitespace under this specification.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Buffer holds at most the current undecided whitespace run.
type Buffer struct {
	pending []Event
}

// New returns an empty whitespace buffer.
func New() *Buffer { return &Buffer{} }

// Add appends one whitespace byte.
func (b *Buffer) Add(c byte, offset int) {
	b.pending = append(b.pending, Event{Byte: c, Offset: offset})
}

// Pending reports the current run length.
func (b *Buffer) Pending() int { return len(b.pending) }

// Start reports the first buffered original offset, or offset if empty.
func (b *Buffer) Start(offset int) int {
	if len(b.pending) == 0 {
		return offset
	}
	return b.pending[0].Offset
}

// Keep confirms the run is not at a line ending and returns its bytes.
func (b *Buffer) Keep() []Event {
	events := b.pending
	b.pending = nil
	return events
}

// Drop confirms the run is line trailing and returns the deleted events.
func (b *Buffer) Drop() []Event {
	events := b.pending
	b.pending = nil
	return events
}
