// Package ws buffers a run of spaces/tabs whose fate (trailing or
// mid-line) is only decided by the next line ending or end of stream.
package ws

import "errors"

// ErrOverflow is returned by Add when the pending run exceeds Limit.
var ErrOverflow = errors.New("ws: pending whitespace over limit")

// Buffer holds one pending run of spaces/tabs. It is not safe for
// concurrent use.
type Buffer struct {
	Limit int // max pending bytes; <= 0 means unlimited
	buf   []byte
	start int // input offset of the first pending byte
}

// Add appends one whitespace byte located at input offset pos.
func (b *Buffer) Add(c byte, pos int) error {
	if b.Limit > 0 && len(b.buf) >= b.Limit {
		return ErrOverflow
	}
	if len(b.buf) == 0 {
		b.start = pos
	}
	b.buf = append(b.buf, c)
	return nil
}

// Len returns the number of pending bytes.
func (b *Buffer) Len() int { return len(b.buf) }

// Start returns the input offset of the first pending byte.
func (b *Buffer) Start() int { return b.start }

// Flush releases the pending bytes (they turned out to be mid-line)
// together with their start offset, and resets the buffer.
func (b *Buffer) Flush() ([]byte, int) {
	buf, start := b.buf, b.start
	b.buf = nil
	return buf, start
}

// Drop discards the pending bytes (they turned out to be trailing).
func (b *Buffer) Drop() { b.buf = nil }
