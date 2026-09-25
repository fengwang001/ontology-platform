// Package ws defers the classification of a run of spaces and tabs.
//
// A run is only known to be line-trailing when the next event is a
// line ending or end of stream; any other byte means the run sits in
// the middle of a line and must be emitted verbatim.
package ws

const (
	Space = ' '
	Tab   = '\t'
)

// IsSpace reports whether b is a recognized trailing-whitespace byte.
func IsSpace(b byte) bool { return b == Space || b == Tab }

// Tracker buffers exactly one pending run of spaces/tabs.
// A Tracker must not be used concurrently.
type Tracker struct {
	buf []byte
}

// New returns a fresh Tracker with the given initial capacity.
func New(hint int) *Tracker {
	if hint < 0 {
		hint = 0
	}
	return &Tracker{buf: make([]byte, 0, hint)}
}

// Add extends the pending run.
func (t *Tracker) Add(b byte) { t.buf = append(t.buf, b) }

// Len is the number of buffered whitespace bytes.
func (t *Tracker) Len() int { return len(t.buf) }

// Pending returns the buffered bytes; the slice is invalidated by
// the next Drain, Drop, or Reset call.
func (t *Tracker) Pending() []byte { return t.buf }

// Drain declares the run interior whitespace: it is returned and
// the tracker is cleared.
func (t *Tracker) Drain() []byte {
	run := t.buf
	t.buf = t.buf[:0]
	return run
}

// Drop declares the run line-trailing whitespace: its length is
// returned for offset accounting and the tracker is cleared.
func (t *Tracker) Drop() int {
	n := len(t.buf)
	t.buf = t.buf[:0]
	return n
}

// Reset empties the tracker.
func (t *Tracker) Reset() { t.buf = t.buf[:0] }
