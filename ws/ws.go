// Package ws delays the verdict on runs of spaces and tabs: a run is trailing
// only once a line ending or stream end is observed.
package ws

// IsSpace reports whether b is a trailing-whitespace byte (space or tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run buffers a currently-unresolved run of spaces/tabs. It holds no other
// state; the caller decides what happened before and after the run.
type Run struct{ buf []byte }

// New returns an empty Run.
func New() *Run { return &Run{} }

// Add appends one whitespace byte to the run.
func (r *Run) Add(b byte) { r.buf = append(r.buf, b) }

// Len is the buffered run length in bytes.
func (r *Run) Len() int { return len(r.buf) }

// Bytes returns the buffered content without resolving it.
func (r *Run) Bytes() []byte { return r.buf }

// ResolveTrailing discards the run: a line ending was seen after it.
func (r *Run) ResolveTrailing() []byte {
	dropped := r.buf
	r.buf = r.buf[:0]
	return dropped
}

// ResolveContent releases the run as ordinary content (a non-space byte or
// stream end without a line ending ends the line and makes the run trailing;
// callers distinguish those cases explicitly).
func (r *Run) ResolveContent() []byte {
	out := r.buf
	r.buf = r.buf[:0]
	return out
}

// Reset empties the run without returning it.
func (r *Run) Reset() { r.buf = r.buf[:0] }
