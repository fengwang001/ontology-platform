// Package wire defines the self-describing LZ77 stream byte format.
package wire

import "errors"

const (
	Magic   = "O1Z7"
	Version = byte(1)

	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	ErrMagic          = errors.New("wire: bad magic or version")
	ErrVarint         = errors.New("wire: varint too long or overflow")
	ErrZeroDistance   = errors.New("wire: back-reference distance is zero")
	ErrDistanceOutput = errors.New("wire: distance exceeds emitted bytes")
	ErrDistanceWindow = errors.New("wire: distance exceeds window capacity")
	ErrLengthMismatch = errors.New("wire: end total length mismatch")
	ErrChecksum       = errors.New("wire: checksum mismatch")
	ErrTrailing       = errors.New("wire: trailing bytes after end record")
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrOutputLimit    = errors.New("wire: output size limit exceeded")
)

// CorruptError locates a failure at an absolute byte offset of the stream.
type CorruptError struct {
	Kind error
	Off  int
}

func (e *CorruptError) Error() string { return e.Kind.Error() }
func (e *CorruptError) Unwrap() error { return e.Kind }

// At wraps a sentinel with the stream offset where it was detected.
func At(kind error, off int) error { return &CorruptError{Kind: kind, Off: off} }

// AppendUvarint appends x in LEB128.
func AppendUvarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

// ReadUvarint reads one LEB128 integer. It accepts only 1..10 bytes and
// rejects 64-bit overflow; a short stream yields ErrTruncated.
func ReadUvarint(b []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, i + 1, ErrVarint
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
		if i == 9 {
			return 0, i + 1, ErrVarint
		}
	}
	return 0, len(b), ErrTruncated
}

// Header builds the stream header.
func Header(windowCap, maxChain int) []byte {
	b := []byte(Magic)
	b = append(b, Version)
	b = AppendUvarint(b, uint64(windowCap))
	b = AppendUvarint(b, uint64(maxChain))
	return b
}

// Literal builds a literal-length record (payload follows).
func Literal(n int) []byte {
	b := []byte{TagLiteral}
	return AppendUvarint(b, uint64(n))
}

// Match builds a back-reference record.
func Match(dist, length int) []byte {
	b := []byte{TagMatch}
	b = AppendUvarint(b, uint64(dist))
	b = AppendUvarint(b, uint64(length))
	return b
}

// End builds the trailer with original length and CRC32.
func End(totalLen int, crc uint32) []byte {
	b := []byte{TagEnd}
	b = AppendUvarint(b, uint64(totalLen))
	b = AppendUvarint(b, uint64(crc))
	return b
}
