// Package wire defines the on-the-wire byte format of the LZ77 stream.
package wire

import "errors"

// Record tags.
const (
	TagHeader  byte = 1
	TagLiteral byte = 2
	TagRef     byte = 3
	TagFlush   byte = 4
	TagEnd     byte = 5
)

// MinMatch is the shortest match worth emitting as a back reference.
const MinMatch = 3

// Sentinel corruption reasons; CorruptError carries the byte offset.
var (
	ErrBadMagic       = errors.New("wire: bad magic/version")
	ErrZeroDistance   = errors.New("wire: back-reference distance is zero")
	ErrDistOutput     = errors.New("wire: distance exceeds bytes output")
	ErrDistWindow     = errors.New("wire: distance exceeds window capacity")
	ErrVarintTooLong  = errors.New("wire: varint too long or overflows 64 bits")
	ErrLengthMismatch = errors.New("wire: end record length mismatch")
	ErrChecksum       = errors.New("wire: checksum mismatch")
	ErrTrailingBytes  = errors.New("wire: trailing bytes after end record")
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrBadTag         = errors.New("wire: unknown record tag")
	ErrBadRecord      = errors.New("wire: invalid record field")
)

// CorruptError wraps a corruption reason with the stream byte offset.
type CorruptError struct {
	Offset int
	Err    error
}

func (e *CorruptError) Error() string { return e.Err.Error() }
func (e *CorruptError) Unwrap() error { return e.Err }

func putUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendRec(b []byte, tag byte, vs ...uint64) []byte {
	b = append(b, tag)
	for _, v := range vs {
		b = putUvarint(b, v)
	}
	return b
}

func AppendHeader(b []byte, windowCap int) []byte {
	return appendRec(b, TagHeader, uint64(windowCap))
}

func AppendLiteral(b, lit []byte) []byte {
	b = appendRec(b, TagLiteral, uint64(len(lit)))
	return append(b, lit...)
}

func AppendRef(b []byte, distance, length int) []byte {
	return appendRec(b, TagRef, uint64(distance), uint64(length))
}

func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

func AppendEnd(b []byte, origLen int, checksum uint64) []byte {
	return appendRec(b, TagEnd, uint64(origLen), checksum)
}

// readUvarint consumes one LEB128 integer from b. A 10th byte may carry only
// one bit; anything longer or overflowing is reported at byte offset off.
func readUvarint(b []byte, off int) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, &CorruptError{Offset: off + i, Err: ErrVarintTooLong}
		}
		if i == 10 {
			return 0, 0, &CorruptError{Offset: off + i, Err: ErrVarintTooLong}
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, ErrTruncated
}

// ReadUvarint consumes one LEB128 integer from b at byte offset off.
func ReadUvarint(b []byte, off int) (uint64, int, error) { return readUvarint(b, off) }
