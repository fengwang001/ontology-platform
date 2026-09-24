// Package ws performs deferred classification of horizontal spaces.
//
// A run of spaces and tabs following a line content byte is
// provisional trailing whitespace until the next event decides:
// a line ending or stream end means the run is trailing (drop it);
// any other byte means it is interior (keep it verbatim). The run is
// therefore buffered, never emitted or dropped early, which keeps the
// result independent of how the input is split into writes.
package ws

// Space reports whether b is trailing-eligible horizontal space.
func Space(b byte) bool { return b == ' ' || b == '\t' }

// Buf holds one provisional trailing-whitespace run.
type Buf struct {
	data []byte
}

// Add appends a space byte to the provisional run.
func (b *Buf) Add(c byte) { b.data = append(b.data, c) }

// Len is the run length in bytes.
func (b *Buf) Len() int { return len(b.data) }

// Pending reports whether a run is buffered.
func (b *Buf) Pending() bool { return len(b.data) > 0 }

// Trailing resolves the run as trailing whitespace: it is dropped and
// the buffer is cleared.
func (b *Buf) Trailing() { b.data = b.data[:0] }

// Interior resolves the run as in-line whitespace, returning its
// bytes (kept verbatim) and clearing the buffer.
func (b *Buf) Interior() []byte {
	out := b.data
	b.data = nil
	return out
}

// Bytes returns the buffered run without clearing it.
func (b *Buf) Bytes() []byte { return b.data }
