// Package ws performs lazy trailing-whitespace classification.
// A run of spaces/tabs is buffered until the next byte proves whether it
// sits at a line end (drop) or inside content (flush verbatim).
package ws

// IsSpace reports whether b is a managed trailing-whitespace byte.
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Verdict resolves a buffered whitespace run.
type Verdict uint8

const (
	// None means no whitespace was buffered.
	None Verdict = iota
	// Flush means the run is content and must be emitted verbatim.
	Flush
	// Drop means the run is trailing whitespace and must be deleted.
	Drop
)

// Backtrack returns how many trailing bytes of data are buffered whitespace.
// This is used together with eol.ResyncStart when joining parallel pieces.
func Backtrack(data []byte) int {
	n := 0
	for n < len(data) && IsSpace(data[len(data)-1-n]) {
		n++
	}
	return n
}

// Buffer accumulates an undecided run of spaces/tabs.
type Buffer struct {
	data []byte
	cap_ int
}

// NewBuffer creates a buffer with a maximum pending-run length.
// A non-positive cap disables the limit.
func NewBuffer(cap int) *Buffer {
	if cap > 0 && cap < 8 {
		cap = 8
	}
	return &Buffer{cap_: cap}
}

// Len reports the number of currently buffered bytes.
func (b *Buffer) Len() int { return len(b.data) }

// Add appends one whitespace byte and reports whether the cap was exceeded.
func (b *Buffer) Add(c byte) bool {
	b.data = append(b.data, c)
	return b.cap_ > 0 && len(b.data) > b.cap_
}

// Bytes returns the buffered run.
func (b *Buffer) Bytes() []byte { return b.data }

// Start reports the original offset of the first buffered byte.
func (b *Buffer) Start(firstOff int) int { return firstOff }

// Resolve ends the run: atLine=true drops it, otherwise it is content.
func (b *Buffer) Resolve(atLine bool) Verdict {
	if len(b.data) == 0 {
		return None
	}
	v := Flush
	if atLine {
		v = Drop
	}
	b.data = b.data[:0]
	return v
}
