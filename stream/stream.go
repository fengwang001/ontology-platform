// Package stream provides a cursor-style Reader and a concurrency-safe
// Writer on top of package enc. Reads are single-pass: each byte of the
// buffer is examined exactly once, with zero prescan/lookback.
package stream

import (
	"sync"

	"ontology/enc"
)

// Reader decodes varints sequentially from a byte buffer.
type Reader struct {
	buf []byte
	pos int
	// checked counts bytes examined by decodes (equals consumed bytes:
	// decoding is single-pass). prescan counts extra bytes scanned or
	// re-read to locate a varint's terminator; it is always 0 here.
	// Both are unexported and never leak through the public API.
	checked int
	prescan int
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

// Reset replaces the buffer and rewinds the cursor.
func (r *Reader) Reset(b []byte) {
	r.buf = b
	r.pos = 0
	r.checked = 0
	r.prescan = 0
}

func (r *Reader) Pos() int { return r.pos }
func (r *Reader) Len() int { return len(r.buf) }

// ReadUint decodes one unsigned varint at the cursor. On any rejection
// (empty input, overflow, non-canonical) the cursor does not advance.
func (r *Reader) ReadUint() (uint64, error) {
	v, n, err := enc.DecodeUint(r.buf[r.pos:])
	if err != nil {
		return 0, err
	}
	r.pos += n
	r.checked += n
	return v, nil
}

// ReadInt decodes one signed varint at the cursor; same failure semantics.
func (r *Reader) ReadInt() (int64, error) {
	v, n, err := enc.DecodeInt(r.buf[r.pos:])
	if err != nil {
		return 0, err
	}
	r.pos += n
	r.checked += n
	return v, nil
}

// Writer is a concurrency-safe append-only varint buffer. Each Write is
// atomic: a multi-byte varint is never interleaved with another write.
type Writer struct {
	mu  sync.Mutex
	buf []byte
}

func NewWriter() *Writer { return &Writer{} }

func (w *Writer) WriteUint(v uint64) {
	b := enc.EncodeUint(v)
	w.mu.Lock()
	w.buf = append(w.buf, b...)
	w.mu.Unlock()
}

func (w *Writer) WriteInt(v int64) {
	b := enc.EncodeInt(v)
	w.mu.Lock()
	w.buf = append(w.buf, b...)
	w.mu.Unlock()
}

// Bytes returns a copy of the accumulated buffer.
func (w *Writer) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, len(w.buf))
	copy(out, w.buf)
	return out
}
