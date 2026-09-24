// Package ws defers the verdict on runs of horizontal whitespace.
//
// A run of spaces and tabs is only known to be trailing once a line end
// or stream end is reached. Run accumulates the bytes; EndLine and
// Retain are the two mutually exclusive resolutions. Splitting the input
// anywhere inside a run cannot change the result because resolution only
// depends on the eventual trigger, not on chunk boundaries.
package ws

const (
	// Space is the ASCII space byte.
	Space byte = ' '
	// Tab is the ASCII tab byte.
	Tab byte = '\t'
)

// Is reports whether b is trailing-eligible horizontal whitespace.
func Is(b byte) bool { return b == Space || b == Tab }

// Verdict classifies a resolved whitespace run.
type Verdict uint8

const (
	// Unresolved means no run is currently buffered.
	Unresolved Verdict = iota
	// Trailing means the buffered run sits at a line end and is deleted.
	Trailing
	// Interior means the buffered run sits inside a line and is kept.
	Interior
)

// Run buffers one consecutive whitespace run.
//
// The zero value is ready to use. A Run holds at most one run at a time:
// callers must resolve it (Trailing/Interior) before starting another.
type Run struct {
	buf []byte
}

// Len reports the number of buffered whitespace bytes.
func (r *Run) Len() int { return len(r.buf) }

// Active reports whether a run is currently buffered.
func (r *Run) Active() bool { return len(r.buf) > 0 }

// Add buffers one whitespace byte.
func (r *Run) Add(b byte) { r.buf = append(r.buf, b) }

// Bytes returns the buffered bytes; the slice is valid until the next
// mutation and must not be modified by the caller.
func (r *Run) Bytes() []byte { return r.buf }

// EndLine resolves the buffered run as trailing (deleted at a line end)
// and returns its length. A no-op on an inactive run.
func (r *Run) EndLine() int {
	n := len(r.buf)
	r.buf = r.buf[:0]
	return n
}

// Retain resolves the buffered run as interior whitespace, returning its
// bytes for emission and clearing the run. Returns nil when inactive.
func (r *Run) Retain() []byte {
	if len(r.buf) == 0 {
		return nil
	}
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	r.buf = r.buf[:0]
	return out
}

// Reset clears any buffered run.
func (r *Run) Reset() { r.buf = r.buf[:0] }
