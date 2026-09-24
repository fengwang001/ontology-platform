// Package ws provides delayed classification of trailing whitespace.
// A run of spaces/tabs is buffered until the following byte proves it is
// either trailing (a line ending / end of stream: delete it) or interior
// (any other byte: emit it verbatim). It has no package dependencies.
package ws

// IsSpace reports whether b is a trailing-whitespace byte (space or tab).
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Pending buffers an undecided run of spaces/tabs. The zero value is ready
// with no limit; use Limit to cap the retained run.
type Pending struct {
	buf   []byte
	start int // original byte offset of buf[0], for error reporting
	limit int // <= 0 means unlimited
}

// New returns a Pending buffer allowing at most limit undecided bytes
// (limit <= 0 means unlimited).
func New(limit int) *Pending { return &Pending{limit: limit} }

// Reset clears the buffer and records the original offset of the next byte.
func (p *Pending) Reset(origOffset int) {
	p.buf = p.buf[:0]
	p.start = origOffset
}

// Add appends a whitespace byte. It returns false when the configured limit
// would be exceeded; in that case the byte is not stored and the caller must
// reject the stream (the run cannot be decided safely).
func (p *Pending) Add(b byte) bool {
	if p.limit > 0 && len(p.buf) >= p.limit {
		return false
	}
	p.buf = append(p.buf, b)
	return true
}

// Len is the number of buffered undecided bytes.
func (p *Pending) Len() int { return len(p.buf) }

// Start is the original offset of the first buffered byte.
func (p *Pending) Start() int { return p.start }

// Bytes returns the buffered run (valid until the next mutation).
func (p *Pending) Bytes() []byte { return p.buf }

// Drop discards the run: it was trailing whitespace.
func (p *Pending) Drop() { p.buf = p.buf[:0] }

// Take removes and returns the run: it was interior whitespace.
func (p *Pending) Take() []byte {
	out := append([]byte(nil), p.buf...)
	p.buf = p.buf[:0]
	return out
}

// Run returns the maximal trailing space/tab run immediately before index i
// in b as [start,end). Used when reconciling split boundaries.
func RunBack(b []byte, i int) int {
	s := i
	for s > 0 && IsSpace(b[s-1]) {
		s--
	}
	return s
}

// RunFwd returns the index of the first non-space/tab byte at or after i.
func RunFwd(b []byte, i int) int {
	for i < len(b) && IsSpace(b[i]) {
		i++
	}
	return i
}
