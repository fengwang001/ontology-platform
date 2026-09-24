// Package ws buffers trailing spaces/tabs whose fate (middle-of-line content
// vs. end-of-line trash) is only known once a line ending or EOS is seen.
package ws

// Space and Tab are the only bytes treated as trailing whitespace.
const (
	Space byte = ' '
	Tab   byte = '\t'
)

// Is reports whether b is a deferred whitespace byte.
func Is(b byte) bool { return b == Space || b == Tab }

// Buf holds a not-yet-decided run of spaces/tabs.
// A run starts at the first whitespace byte after emitted line content;
// zero value is ready to use.
type Buf struct {
	data []byte
}

// Len is the number of buffered bytes.
func (b *Buf) Len() int { return len(b.data) }

// Bytes returns the buffered content (read-only until the next mutation).
func (b *Buf) Bytes() []byte { return b.data }

// Active reports whether a run is currently buffered.
func (b *Buf) Active() bool { return len(b.data) > 0 }

// Add appends a whitespace byte to the pending run.
func (b *Buf) Add(c byte) { b.data = append(b.data, c) }

// Keep declares the run middle-of-line content: returns it and clears.
func (b *Buf) Keep() []byte {
	out := b.data
	b.data = b.data[:0]
	return out
}

// Drop declares the run end-of-line whitespace: discards and clears.
func (b *Buf) Drop() { b.data = b.data[:0] }

// Reset empties the buffer.
func (b *Buf) Reset() { b.data = b.data[:0] }
