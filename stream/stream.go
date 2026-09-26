// Package stream provides cursor-based varint readers and a concurrency-safe
// varint writer on top of package enc.
package stream

import (
	"sync"

	"ontology/enc"
)

// Reader reads unsigned and signed varints sequentially from a byte slice.
// A rejected read never advances the cursor.
type Reader struct {
	b   []byte
	pos int

	// lastExtra counts bytes additionally prescanned/reviewed (beyond the
	// single forward decoding pass) to locate the end byte of the most
	// recent varint. It stays 0 because decoding terminates the moment it
	// sees the end byte: there is no separate prescan and no look-back
	// beyond the byte under the cursor. Unexported by design; it must never
	// be reachable through the public API.
	lastExtra int
}

// NewReader returns a Reader over b.
func NewReader(b []byte) *Reader {
	return &Reader{b: b}
}

// ReadUint reads one unsigned varint at the cursor. The cursor advances only
// on success, so a rejected (empty/overflow/non-canonical) read leaves the
// reader ready to read the same bytes again.
func (r *Reader) ReadUint() (uint64, error) {
	v, n, err := enc.DecodeUint(r.b[r.pos:])
	if err != nil {
		r.lastExtra = 0
		return 0, err
	}
	r.pos += n
	r.lastExtra = 0 // every byte of the varint was touched exactly once
	return v, nil
}

// ReadInt reads one signed varint at the cursor with the same no-advance-on-
// error guarantee as ReadUint.
func (r *Reader) ReadInt() (int64, error) {
	v, n, err := enc.DecodeInt(r.b[r.pos:])
	if err != nil {
		r.lastExtra = 0
		return 0, err
	}
	r.pos += n
	r.lastExtra = 0
	return v, nil
}

// Pos returns the current cursor position.
func (r *Reader) Pos() int { return r.pos }

// Len returns the total length of the underlying buffer.
func (r *Reader) Len() int { return len(r.b) }

// Reset replaces the buffer and rewinds the cursor.
func (r *Reader) Reset(b []byte) {
	r.b = b
	r.pos = 0
	r.lastExtra = 0
}

// Writer accumulates varints. It is safe for concurrent use: the whole
// multi-byte encoding of one value is appended while holding the lock, so
// bytes from different goroutines can never interleave.
type Writer struct {
	mu  sync.Mutex
	buf []byte
}

// NewWriter returns an empty Writer.
func NewWriter() *Writer { return &Writer{} }

// WriteUint appends the unsigned varint encoding of v atomically.
func (w *Writer) WriteUint(v uint64) {
	b := enc.EncodeUint(v)
	w.mu.Lock()
	w.buf = append(w.buf, b...)
	w.mu.Unlock()
}

// WriteInt appends the signed varint encoding of v atomically.
func (w *Writer) WriteInt(v int64) {
	b := enc.EncodeInt(v)
	w.mu.Lock()
	w.buf = append(w.buf, b...)
	w.mu.Unlock()
}

// Bytes returns a copy of the bytes written so far.
func (w *Writer) Bytes() []byte {
	w.mu.Lock()
	out := append([]byte(nil), w.buf...)
	w.mu.Unlock()
	return out
}
