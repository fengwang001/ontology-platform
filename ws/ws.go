// Package ws defers the decision for a run of spaces and tabs: the run
// is trailing whitespace only once a line ending or end of stream is seen.
package ws

// Action tells the consumer what to do with an event byte.
type Action int

const (
	// Pass emits the byte (after taking the flushed run, if any).
	Pass Action = iota
	// Buffer holds the byte back in the pending whitespace run.
	Buffer
	// Drop discards the whole pending run (read Take); the '\n' is emitted.
	Drop
)

// Buf tracks a pending run of spaces/tabs with a byte capacity.
type Buf struct {
	buf    []byte
	limit  int
	broken bool
}

// New creates a buffer with the given maximum byte capacity (<=0 = unlimited).
func New(limit int) *Buf { return &Buf{limit: limit} }

// Observe classifies a byte. ' ' and '\t' are buffered; '\n' (the
// normalized line ending) drops the pending run (read Take); everything
// else flushes it first (read Take). A run exceeding the capacity reports
// ok=false exactly once and does not change state on that call.
func (b *Buf) Observe(c byte) (act Action, ok bool) {
	switch {
	case c == ' ' || c == '\t':
		if b.limit > 0 && len(b.buf) >= b.limit {
			b.broken = true
			return Pass, false
		}
		b.buf = append(b.buf, c)
		return Buffer, true
	case c == '\n':
		b.buf = b.buf[:0]
		return Drop, true
	default:
		b.buf = b.buf[:0]
		return Pass, true
	}
}

// Take returns and clears the pending run.
func (b *Buf) Take() []byte {
	flush := b.buf
	b.buf = b.buf[:0]
	return flush
}

// Pending reports the buffered run length.
func (b *Buf) Pending() int { return len(b.buf) }
