// Package ws defers the verdict on runs of spaces and tabs: a run is trailing
// whitespace only if a line ending or stream end follows it.
package ws

// IsSpace reports whether b is a deferred-whitespace byte (space or tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run accumulates a not-yet-decided run of spaces/tabs together with the
// original byte offset of its first byte.
type Run struct {
	Bytes []byte // buffered spaces and tabs
	Start int    // original offset of the first byte
}

// Reset empties the run.
func (r *Run) Reset() { r.Bytes = r.Bytes[:0]; r.Start = 0 }

// Len returns the buffered byte count.
func (r *Run) Len() int { return len(r.Bytes) }

// Add appends one space/tab at original offset off. A fresh run records off.
func (r *Run) Add(b byte, off int) {
	if len(r.Bytes) == 0 {
		r.Start = off
	}
	r.Bytes = append(r.Bytes, b)
}

// Active reports whether a run is being accumulated.
func (r *Run) Active() bool { return len(r.Bytes) > 0 }
