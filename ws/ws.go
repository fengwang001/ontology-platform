// Package ws tracks a run of trailing spaces/tabs whose fate (deleted as
// trailing whitespace, or emitted as interior whitespace) is known only
// when the next byte or end of stream is seen.
package ws

// IsSpace reports whether b is a space or a tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run buffers a not-yet-decided whitespace run. The zero value is ready.
// It is not safe for concurrent use.
type Run struct {
	// Start is the original offset of the first buffered whitespace byte.
	Start int
	// Bytes holds the buffered whitespace.
	Bytes []byte
}

// Active reports whether whitespace is currently buffered.
func (r *Run) Active() bool { return len(r.Bytes) > 0 }

// Len returns the number of buffered whitespace bytes.
func (r *Run) Len() int { return len(r.Bytes) }

// Add buffers one whitespace byte; startOrig is its original offset. The
// start of the first byte in a run is remembered.
func (r *Run) Add(b byte, startOrig int) {
	if len(r.Bytes) == 0 {
		r.Start = startOrig
	}
	r.Bytes = append(r.Bytes, b)
}

// Take removes and returns the buffered whitespace, clearing the run.
func (r *Run) Take() []byte {
	out := r.Bytes
	r.Bytes = nil
	return out
}

// Clear drops the buffered whitespace.
func (r *Run) Clear() { r.Bytes = nil }
