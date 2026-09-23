// Package ws tracks a run of trailing spaces/tabs whose fate is not known
// yet: it becomes trailing whitespace (deleted) when a line ending or
// stream end arrives, or ordinary content (emitted) when any other byte
// arrives.
package ws

// Is reports whether b is a trailing-whitespace candidate: space or tab.
func Is(b byte) bool { return b == ' ' || b == '\t' }

// Pending buffers one undecided run of spaces/tabs plus the original
// offset where the run starts.
type Pending struct {
	buf []byte
	// start is the original byte offset of buf[0]; -1 when empty.
	start int
}

// New returns an empty tracker; cap avoids repeated allocation for the
// pending run (0 means unbounded growth, bounded by the caller).
func New(cap int) *Pending {
	return &Pending{start: -1, buf: make([]byte, 0, cap)}
}

// Start returns the original offset of the run or -1 when empty.
func (p *Pending) Start() int { return p.start }

// Len returns the number of buffered bytes.
func (p *Pending) Len() int { return len(p.buf) }

// Empty reports whether nothing is buffered.
func (p *Pending) Empty() bool { return len(p.buf) == 0 }

// Add appends a whitespace byte. origStart is remembered only for the
// first byte of the run.
func (p *Pending) Add(b byte, origOffset int) {
	if len(p.buf) == 0 {
		p.start = origOffset
	}
	p.buf = append(p.buf, b)
}

// Take returns the buffered bytes (valid until the next mutation) and
// clears the run.
func (p *Pending) Take() []byte {
	out := p.buf
	p.buf = p.buf[:0]
	p.start = -1
	return out
}

// Clear drops the run without returning it.
func (p *Pending) Clear() {
	p.buf = p.buf[:0]
	p.start = -1
}
