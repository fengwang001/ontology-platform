// Package ws decides whether runs of spaces/tabs are trailing whitespace.
// A run is undecided until a line ending or end of stream proves it trailing,
// or any other byte proves it intra-line.
package ws

// IsTrailingByte reports whether b starts/continues a line-ending decision
// (CR or LF). Such a byte makes the currently buffered run trailing.
func IsTrailingByte(b byte) bool { return b == '\r' || b == '\n' }

// IsSpace reports whether b belongs to a deferrable whitespace run.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Run is a deferred run of spaces/tabs with its original byte range.
type Run struct {
	Data  []byte
	Start int // original offset of the first byte
}

// Append extends the run.
func (r *Run) Append(b byte, off int) {
	if len(r.Data) == 0 {
		r.Start = off
	}
	r.Data = append(r.Data, b)
}

// Len is the deferred byte count.
func (r *Run) Len() int { return len(r.Data) }

// Reset empties the run.
func (r *Run) Reset() { r.Data = r.Data[:0]; r.Start = 0 }
