// Package wire defines the self-describing LZ77 stream byte format.
// It must not depend on any other package in this module.
package wire

import (
	"encoding/binary"
	"errors"
	"hash"
	"hash/fnv"
)

// Magic is the 5-byte stream header; Version is the only supported version.
const (
	Magic   = "ONTLZ"
	Version = byte(1)
	Header  = Magic + "\x01"
)

// Record tags.
const (
	TagLit byte = iota
	TagMatch
	TagFlush
	TagEnd
)

// Sentinel errors; OffsetError carries the byte offset inside the stream.
var (
	ErrMagic     = errors.New("wire: bad magic")
	ErrVersion   = errors.New("wire: unsupported version")
	ErrVarint    = errors.New("wire: varint too long or overflows uint64")
	ErrTag       = errors.New("wire: unknown record tag")
	ErrTruncated = errors.New("wire: truncated stream")
)

// OffsetError wraps a format error with the stream byte offset where it occurred.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// At wraps err with offset unless it already carries one.
func At(err error, offset int) error {
	if err == nil {
		return nil
	}
	var oe *OffsetError
	if errors.As(err, &oe) {
		return err
	}
	return &OffsetError{Err: err, Offset: offset}
}

// PutUvarint appends an ULEB128-encoded value to b.
func PutUvarint(b []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(b, buf[:n]...)
}

// Reader tracks the absolute byte offset consumed from the compressed stream.
type Reader struct {
	B      []byte
	Offset int
}

// Byte reads one byte; a short read reports ErrTruncated at the current offset.
func (r *Reader) Byte() (byte, error) {
	if r.Offset >= len(r.B) {
		return 0, At(ErrTruncated, r.Offset)
	}
	b := r.B[r.Offset]
	r.Offset++
	return b, nil
}

// Bytes reads n raw bytes.
func (r *Reader) Bytes(n int) ([]byte, error) {
	if n < 0 || r.Offset+n > len(r.B) {
		return nil, At(ErrTruncated, r.Offset)
	}
	s := r.B[r.Offset : r.Offset+n]
	r.Offset += n
	return s, nil
}

// Uvarint reads at most 10 bytes and rejects overflow / oversized encodings.
func (r *Reader) Uvarint() (uint64, error) {
	var x uint64
	for i := 0; i < binary.MaxVarintLen64; i++ {
		b, err := r.Byte()
		if err != nil {
			return 0, err
		}
		if b < 0x80 {
			if i == binary.MaxVarintLen64-1 && b > 1 {
				return 0, At(ErrVarint, r.Offset)
			}
			return x | uint64(b)<<(7*i), nil
		}
		x |= uint64(b&0x7f) << (7 * i)
	}
	return 0, At(ErrVarint, r.Offset)
}

// Header validates magic and version, returning ErrMagic/ErrVersion with offset.
func HeaderValid(b []byte) error {
	if len(b) < len(Header) {
		return At(ErrTruncated, 0)
	}
	if string(b[:len(Magic)]) != Magic {
		return At(ErrMagic, 0)
	}
	if b[len(Magic)] != Version {
		return At(ErrVersion, len(Magic))
	}
	return nil
}

// NewHash returns the streaming FNV-1a 64 digest used by trailer and checks.
func NewHash() hash.Hash64 { return fnv.New64a() }
