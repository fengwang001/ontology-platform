// Package wire defines the byte format of the compressed stream:
// header, literal runs, backrefs, flush marks and the end record.
// All multi-byte integers are uvarints (LEB128).
package wire

import "errors"

const (
	Magic0   = 'O'
	Magic1   = 'L'
	Magic2   = 'Z'
	Version  = 1
	HeaderLen = 4
)

// Record tags.
const (
	TagLiteral = 0
	TagBackref = 1
	TagFlush   = 2
	TagEnd     = 3
)

// ErrVarintOverflow reports a varint longer than 10 bytes or overflowing 64 bits.
var ErrVarintOverflow = errors.New("wire: varint exceeds 10 bytes or overflows 64 bits")

// Distinguishable stream-format violation sentinels. The decoder wraps them
// in an error type that also carries the compressed-stream byte offset.
var (
	ErrBadMagic         = errors.New("wire: bad stream magic")
	ErrBadVersion       = errors.New("wire: unsupported stream version")
	ErrBadTag           = errors.New("wire: unknown record tag")
	ErrZeroDistance     = errors.New("wire: backref distance is zero")
	ErrDistanceTooLarge = errors.New("wire: backref distance exceeds window capacity")
	ErrDistanceTooFar   = errors.New("wire: backref distance exceeds bytes already output")
	ErrLengthMismatch   = errors.New("wire: end record length mismatch")
	ErrChecksumMismatch = errors.New("wire: end record checksum mismatch")
	ErrTrailingData     = errors.New("wire: bytes after end of stream")
	ErrTruncated        = errors.New("wire: truncated stream")
	ErrOutputLimit      = errors.New("wire: output limit exceeded")
)

// AppendUvarint appends v to dst in LEB128 form.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// Uvarint decodes one varint from b. It returns n == 0 and a nil error when b
// is a proper prefix of a varint, i.e. more input bytes are needed.
func Uvarint(b []byte) (v uint64, n int, err error) {
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, ErrVarintOverflow
		}
		if c < 0x80 {
			return v | uint64(c)<<uint(7*i), i + 1, nil
		}
		v |= uint64(c&0x7f) << uint(7*i)
	}
	if len(b) >= 10 {
		return 0, 0, ErrVarintOverflow
	}
	return 0, 0, nil
}

// AppendHeader appends the stream header.
func AppendHeader(dst []byte) []byte {
	return append(dst, Magic0, Magic1, Magic2, Version)
}

// AppendLiteral appends a literal run record.
func AppendLiteral(dst, lit []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(lit)))
	return append(dst, lit...)
}

// AppendBackref appends a backref record (distance + length).
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = append(dst, TagBackref)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush appends a flush mark.
func AppendFlush(dst []byte) []byte {
	return append(dst, TagFlush)
}

// AppendEnd appends the end record: total input length and checksum.
func AppendEnd(dst []byte, total, sum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// FNV-1a 64 constants.
const (
	sumOffset = 14695981039346656037
	sumPrime  = 1099511628211
)

// Sum is a running FNV-1a 64 checksum over the original bytes.
type Sum struct{ v uint64 }

// NewSum returns an initialized checksum.
func NewSum() Sum { return Sum{v: sumOffset} }

// Add folds one byte into the checksum.
func (s *Sum) Add(b byte) {
	s.v ^= uint64(b)
	s.v *= sumPrime
}

// AddBytes folds p into the checksum.
func (s *Sum) AddBytes(p []byte) {
	for _, b := range p {
		s.Add(b)
	}
}

// Value returns the current checksum.
func (s *Sum) Value() uint64 { return s.v }
