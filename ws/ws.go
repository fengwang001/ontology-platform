// Package ws performs deferred classification of trailing spaces/tabs.
package ws

// IsSpace reports whether b is a trailing-eligible space or tab.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run buffers a not-yet-classified run of spaces and tabs.
// A run is trailing only if the next event is a line end or stream EOF.
type Run struct {
	start int
	buf   []byte
}

// Add appends one space/tab at original offset pos and reports whether the
// configured capacity is exceeded.
func (r *Run) Add(b byte, pos, cap int) bool {
	if len(r.buf) == 0 {
		r.start = pos
	}
	if len(r.buf) >= cap {
		return true
	}
	r.buf = append(r.buf, b)
	return false
}

// Active reports whether spaces are currently buffered.
func (r *Run) Active() bool { return len(r.buf) > 0 }

// Len is the number of buffered bytes.
func (r *Run) Len() int { return len(r.buf) }

// Start is the original offset of the first buffered byte.
func (r *Run) Start() int { return r.start }

// Bytes returns the buffered content.
func (r *Run) Bytes() []byte { return r.buf }

// Clear discards the buffered run (it was classified as trailing).
func (r *Run) Clear() { r.buf = r.buf[:0] }

// Take moves the buffered content out and resets the run (it was classified
// as mid-line whitespace that must be emitted).
func (r *Run) Take() []byte {
	out := r.buf
	r.buf = nil
	return out
}
