// Package wire defines the byte format of the custom LZ77 stream:
// header, literal runs, backrefs, flush marks and the stream tail.
// All integers use unsigned LEB128 varint encoding (max 10 bytes).
package wire

import "errors"

const (
	Magic     = "LZ77"
	Version   = 1
	HeaderLen = len(Magic) + 1
)

// Record tags.
const (
	TagLiteral byte = 0x01
	TagBackref byte = 0x02
	TagFlush   byte = 0x03
	TagEnd     byte = 0x04
)

var (
	// ErrIncomplete means the buffer ended before the varint did.
	ErrIncomplete = errors.New("wire: incomplete varint")
	// ErrOverflow means the varint exceeds 10 bytes or 64 bits.
	ErrOverflow = errors.New("wire: varint overflow")
)

// AppendUvarint encodes v as unsigned LEB128.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// Uvarint decodes one varint from b, returning the value and the number
// of bytes consumed. It returns ErrIncomplete if b ends mid-varint and
// ErrOverflow if the encoding exceeds 10 bytes or 64 bits.
func Uvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(b) >= 10 {
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrIncomplete
}

// AppendHeader appends the stream header.
func AppendHeader(dst []byte) []byte {
	return append(dst, Magic[0], Magic[1], Magic[2], Magic[3], Version)
}

// AppendLiteral appends a literal run record; empty runs emit nothing.
func AppendLiteral(dst, p []byte) []byte {
	if len(p) == 0 {
		return dst
	}
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

// AppendBackref appends a backref record (dist, length).
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = append(dst, TagBackref)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush appends a flush mark.
func AppendFlush(dst []byte) []byte {
	return append(dst, TagFlush)
}

// AppendEnd appends the stream tail: original total length and checksum.
func AppendEnd(dst []byte, total, sum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// Sum64 is the stream checksum: FNV-1a 64.
type Sum64 uint64

const (
	sumOffset = 14695981039346656037
	sumPrime  = 1099511628211
)

// NewSum64 returns the initial checksum state.
func NewSum64() Sum64 { return sumOffset }

// Add folds p into the checksum.
func (s *Sum64) Add(p []byte) {
	for _, b := range p {
		*s = (*s ^ Sum64(b)) * sumPrime
	}
}

// Checksum returns the FNV-1a 64 checksum of p.
func Checksum(p []byte) uint64 {
	s := NewSum64()
	s.Add(p)
	return uint64(s)
}
