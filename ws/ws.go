// Package ws defers the decision about a run of spaces/tabs: only a line
// ending or end of stream proves that the run was trailing whitespace.
package ws

// Tracker holds the currently undecided run of trailing whitespace.
type Tracker struct {
	buf []byte
}

// IsSpace reports whether b is a space or tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Pending reports that a whitespace run is buffered.
func (t *Tracker) Pending() bool { return len(t.buf) > 0 }

// Len is the number of buffered whitespace bytes.
func (t *Tracker) Len() int { return len(t.buf) }

// Add appends a whitespace byte.
func (t *Tracker) Add(b byte) { t.buf = append(t.buf, b) }

// Keep releases the run as ordinary (in-line) whitespace: the caller must emit
// the returned bytes, then the tracker is empty and ready for more.
func (t *Tracker) Keep() []byte {
	out := t.buf
	t.buf = nil
	return out
}

// Drop discards the run (it was trailing whitespace).
func (t *Tracker) Drop() { t.buf = nil }

// Reset empties the tracker.
func (t *Tracker) Reset() { t.buf = nil }
