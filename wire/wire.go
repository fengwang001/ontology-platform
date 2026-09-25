// Package wire defines the byte format of the compression stream:
// header, literal runs, backrefs, flush marker and trailer, all
// integers uvarint-encoded. It has no dependencies.
package wire

import (
	"errors"
	"fmt"
)

const (
	Magic0  = 0x4C // 'L'
	Magic1  = 0x5A // 'Z'
	Version = 0x01
)

const (
	TagLiteral = 0x01 // uvarint(n) + n raw bytes
	TagBackref = 0x02 // uvarint(dist) + uvarint(len)
	TagFlush   = 0x03 // no payload
	TagTrailer = 0x04 // uvarint(totalLen) + uvarint(checksum)
)

// Sentinel error kinds; distinguishable via errors.Is.
var (
	ErrBadMagic         = errors.New("wire: bad magic")
	ErrBadVersion       = errors.New("wire: bad version")
	ErrUnknownTag       = errors.New("wire: unknown record tag")
	ErrDistZero         = errors.New("wire: backref distance is zero")
	ErrDistBeyondOutput = errors.New("wire: backref distance exceeds output")
	ErrDistBeyondWindow = errors.New("wire: backref distance exceeds window")
	ErrVarintOverflow   = errors.New("wire: varint overflow")
	ErrLengthMismatch   = errors.New("wire: trailer length mismatch")
	ErrChecksumMismatch = errors.New("wire: trailer checksum mismatch")
	ErrTrailingData     = errors.New("wire: data after trailer")
	ErrTruncated        = errors.New("wire: stream truncated")
	ErrOutputLimit      = errors.New("wire: output limit exceeded")
)

// Error is a format error annotated with the absolute byte offset
// in the compressed stream where it was detected.
type Error struct {
	Kind error
	Off  int
}

func (e *Error) Error() string { return fmt.Sprintf("%v at offset %d", e.Kind, e.Off) }
func (e *Error) Unwrap() error { return e.Kind }

// AppendHeader appends the 3-byte stream header.
func AppendHeader(dst []byte) []byte {
	return append(dst, Magic0, Magic1, Version)
}

// AppendUvarint appends v in uvarint encoding.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendLiteral appends a literal record.
func AppendLiteral(dst, p []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

// AppendBackref appends a backref record.
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = append(dst, TagBackref)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush appends a flush marker.
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

// AppendTrailer appends the trailer record.
func AppendTrailer(dst []byte, total, sum uint64) []byte {
	dst = append(dst, TagTrailer)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// Varint is an incremental uvarint decoder fed byte by byte.
type Varint struct {
	v uint64
	n int
}

// Feed consumes one byte. On completion it returns the value with
// done=true and resets itself. More than 10 bytes, or a 10th byte > 1,
// is ErrVarintOverflow.
func (r *Varint) Feed(b byte) (v uint64, done bool, err error) {
	if r.n > 9 || r.n == 9 && b > 1 {
		return 0, false, ErrVarintOverflow
	}
	r.v |= uint64(b&0x7f) << (7 * r.n)
	r.n++
	if b >= 0x80 {
		return 0, false, nil
	}
	v, r.v, r.n = r.v, 0, 0
	return v, true, nil
}

// Hash is an incremental FNV-1a 64-bit hasher.
type Hash struct{ v uint64 }

func NewHash() Hash { return Hash{v: 14695981039346656037} }

func (h *Hash) Add(b byte) {
	h.v ^= uint64(b)
	h.v *= 1099511628211
}

// AddBytes hashes a slice of bytes.
func (h *Hash) AddBytes(p []byte) {
	for _, b := range p {
		h.Add(b)
	}
}

func (h *Hash) Sum64() uint64 { return h.v }
