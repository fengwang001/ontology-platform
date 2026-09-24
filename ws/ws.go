// Package ws defers the classification of trailing spaces and tabs: a run of
// whitespace is only known to be trailing once a line ending or stream end is
// seen. It has no dependencies on other packages.
package ws

// IsSpace reports whether c is a trailing-whitespace byte (space or tab).
func IsSpace(c byte) bool { return c == ' ' || c == '\t' }

// Buf accumulates a run of not-yet-classified spaces/tabs.
//
// Typical use: push ordinary bytes through Add while buffering whitespace;
// call Flush when a non-whitespace byte or a line ending resolves the run;
// Drop discards it when it is trailing; Pending inspects unresolved bytes at
// stream end.
type Buf struct {
	b     []byte
	limit int
}

// NewBuf returns a buffer; limit <= 0 means unlimited.
func NewBuf(limit int) *Buf { return &Buf{limit: limit} }

// Len is the number of buffered whitespace bytes.
func (b *Buf) Len() int { return len(b.b) }

// Limit reports the configured limit (0 means unlimited).
func (b *Buf) Limit() int { return b.limit }

// Add buffers one whitespace byte. It returns false when the configured
// limit would be exceeded; the byte is not stored in that case.
func (b *Buf) Add(c byte) bool {
	if b.limit > 0 && len(b.b) >= b.limit {
		return false
	}
	b.b = append(b.b, c)
	return true
}

// Flush appends the buffered run to out (classification: not trailing) and
// empties the buffer. It returns the extended slice.
func (b *Buf) Flush(out []byte) []byte {
	out = append(out, b.b...)
	b.b = b.b[:0]
	return out
}

// Drop discards the buffered run (classification: trailing whitespace).
func (b *Buf) Drop() { b.b = b.b[:0] }

// Bytes returns the buffered bytes without clearing them.
func (b *Buf) Bytes() []byte { return b.b }

// Reset empties the buffer.
func (b *Buf) Reset() { b.b = b.b[:0] }
