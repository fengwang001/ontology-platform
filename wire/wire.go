// Package wire defines the self-describing byte format of the compressed stream.
// It depends on no other package in this module.
package wire

import "errors"

const (
	MinMatch  = 3
	MaxMatch  = 1 << 15
	MaxVarint = 10
)

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	Magic   = [4]byte{0x4F, 0x4E, 0x54, 0x31}
	Version = byte(1)
	EndByte = byte(0)
)

// Sentinel errors; offsets are carried by OffsetError wrappers in the decoder.
var (
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrBadMagic       = errors.New("wire: bad magic or version")
	ErrZeroDistance   = errors.New("wire: back-reference distance is zero")
	ErrDistOutOfWin   = errors.New("wire: distance exceeds window capacity")
	ErrDistNotOutput  = errors.New("wire: distance exceeds bytes already output")
	ErrVarintOverflow = errors.New("wire: varint longer than 10 bytes or overflows uint64")
	ErrLengthMismatch = errors.New("wire: end marker length does not match output")
	ErrChecksum       = errors.New("wire: checksum mismatch")
	ErrTrailingBytes  = errors.New("wire: trailing bytes after end marker")
	ErrOutputLimit    = errors.New("wire: output size limit exceeded")
	ErrClosed         = errors.New("wire: decoder is in terminal error state")
)

// AppendUvarint appends an unsigned LEB128 integer.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint decodes one uvarint from b starting at off.
// It returns the value, the offset just past it and ErrTruncated/ErrVarintOverflow.
func ReadUvarint(b []byte, off int) (uint64, int, error) {
	var v uint64
	for i := 0; i < MaxVarint; i++ {
		if off >= len(b) {
			return 0, off, ErrTruncated
		}
		c := b[off]
		off++
		if i == MaxVarint-1 && (c > 1) {
			return 0, off, ErrVarintOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, off, nil
		}
	}
	return 0, off, ErrVarintOverflow
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// FNV64 folds data into the running FNV-1a 64-bit checksum.
func FNV64(h uint64, p []byte) uint64 {
	for _, c := range p {
		h ^= uint64(c)
		h *= fnvPrime
	}
	return h
}
