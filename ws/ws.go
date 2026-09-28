// Package ws delays the decision for a run of spaces and tabs:
// only a line ending or end of stream tells whether the run is trailing.
package ws

// IsTrailingByte reports whether b is eligible trailing whitespace (' ' or '\t').
func IsTrailingByte(b byte) bool { return b == ' ' || b == '\t' }

// Run buffers one not-yet-decided run of spaces/tabs together with the
// original byte offset where the run starts.
type Run struct {
	data  []byte
	start int
	limit int // <= 0 means unlimited
}

// NewRun creates a run with start offset and an optional byte limit.
func NewRun(start, limit int) Run { return Run{start: start, limit: limit} }

// Start returns the original offset where the run began.
func (r *Run) Start() int { return r.start }

// Len returns the number of buffered bytes.
func (r *Run) Len() int { return len(r.data) }

// Bytes returns the buffered whitespace (nil when empty).
func (r *Run) Bytes() []byte {
	if len(r.data) == 0 {
		return nil
	}
	return r.data
}

// Active reports whether at least one whitespace byte is buffered.
func (r *Run) Active() bool { return len(r.data) > 0 }

// Append buffers another whitespace byte at original offset off.
// It returns false when a positive limit would be exceeded.
func (r *Run) Append(b byte, off int) bool {
	if r.limit > 0 && len(r.data) >= r.limit {
		return false
	}
	if len(r.data) == 0 {
		r.start = off
	}
	r.data = append(r.data, b)
	return true
}

// Reset empties the run.
func (r *Run) Reset() { r.data = r.data[:0] }
