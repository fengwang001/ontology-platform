// Package ws defers the verdict on runs of spaces and tabs: a run is
// trailing whitespace only when followed by a line ending or EOF.
package ws

// IsSpace reports whether b is a deferrable trailing-whitespace byte.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Tracker buffers an undecided run of spaces/tabs.
type Tracker struct {
	buf    []byte
	offset int // source offset of buf[0]
}

// Add appends one whitespace byte at source offset off.
func (t *Tracker) Add(b byte, off int) {
	if len(t.buf) == 0 {
		t.offset = off
	}
	t.buf = append(t.buf, b)
}

// Pending returns the buffered run and its source offset; ok is false if empty.
func (t *Tracker) Pending() (buf []byte, offset int, ok bool) {
	return t.buf, t.offset, len(t.buf) > 0
}

// Len is the number of buffered whitespace bytes.
func (t *Tracker) Len() int { return len(t.buf) }

// Flush hands the buffered run to the caller and clears it.
func (t *Tracker) Flush() []byte {
	b := t.buf
	t.buf = nil
	return b
}

// Drop discards the buffered run (it was trailing whitespace).
func (t *Tracker) Drop() { t.buf = nil }

// Reset clears all state.
func (t *Tracker) Reset() { t.buf = nil }
