// Package ws tracks a trailing run of spaces and tabs.
// The run is only known to be end-of-line whitespace once a line ending
// or stream end is observed, so it is buffered until then.
package ws

// Tracker accumulates a pending run of ' ' and '\t' bytes.
// Zero value is ready.
type Tracker struct {
	buf []byte
	// lim is the maximum pending bytes; 0 means unlimited.
	lim int
}

// New returns a tracker with the given pending-run limit (0 = unlimited).
func New(limit int) *Tracker { return &Tracker{lim: limit} }

// IsWS reports whether b is a buffered whitespace byte.
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Add appends one whitespace byte. It reports false if the run exceeds
// the configured limit; the byte is not stored in that case.
func (t *Tracker) Add(b byte) bool {
	if t.lim > 0 && len(t.buf) >= t.lim {
		return false
	}
	t.buf = append(t.buf, b)
	return true
}

// Pending reports the buffered run.
func (t *Tracker) Pending() []byte { return t.buf }

// Len reports the buffered run length.
func (t *Tracker) Len() int { return len(t.buf) }

// Keep flushes the run as ordinary content and returns its bytes.
func (t *Tracker) Keep() []byte {
	b := t.buf
	t.buf = nil
	return b
}

// Drop discards the run (it was end-of-line whitespace).
func (t *Tracker) Drop() { t.buf = nil }

// Reset returns the tracker to its zero state.
func (t *Tracker) Reset() { t.buf = nil }
