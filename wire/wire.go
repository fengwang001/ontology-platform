package wire

import "errors"

const (
	Magic0   = 'L'
	Magic1   = 'Z'
	Magic2   = '7'
	Version  = 1
	TagLit   = 0x00
	TagMatch = 0x01
	TagFlush = 0x02
	TagEnd   = 0x7E
)

// Sentinel errors. All stream-level failures are one of these.
var (
	ErrBadHeader      = errors.New("wire: bad magic or version")
	ErrVarintTooLong  = errors.New("wire: varint exceeds 10 bytes")
	ErrVarintOverflow = errors.New("wire: varint overflows 64 bits")
	ErrZeroDistance   = errors.New("wire: back-reference distance is zero")
	ErrDistOverOutput = errors.New("wire: distance exceeds bytes produced so far")
	ErrDistOverWindow = errors.New("wire: distance exceeds window capacity")
	ErrBadLength      = errors.New("wire: declared length exceeds remaining/allowed bytes")
	ErrBadTag         = errors.New("wire: unknown record tag")
	ErrLengthMismatch = errors.New("wire: tail total length mismatch")
	ErrChecksum       = errors.New("wire: tail checksum mismatch")
	ErrTrailingBytes  = errors.New("wire: trailing bytes after stream tail")
	ErrTruncated      = errors.New("wire: truncated stream")
)

// OffsetError wraps a failure with the byte offset in the compressed stream.
type OffsetError struct {
	Err    error
	Offset int64
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// AppendUvarint appends an unsigned LEB128 integer.
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint decodes one varint starting at b[0]. used is the consumed byte
// count. It enforces the 10-byte limit and 64-bit range.
func ReadUvarint(b []byte) (v uint64, used int, err error) {
	var shift uint
	for used = 0; used < len(b); used++ {
		c := b[used]
		if used == 9 {
			if c > 1 {
				return 0, used + 1, ErrVarintOverflow
			}
		}
		if used == 10 {
			return 0, 10, ErrVarintTooLong
		}
		v |= uint64(c&0x7F) << shift
		if c < 0x80 {
			return v, used + 1, nil
		}
		shift += 7
	}
	return 0, 0, ErrTruncated
}
