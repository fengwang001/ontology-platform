package wire

import "errors"

const (
	Magic       uint64 = 0x4f4e5a37
	Version     uint64 = 1
	TagLiteral  byte   = 1
	TagMatch    byte   = 2
	TagFlush    byte   = 3
	TagTrailer  byte   = 4
	MaxVarintLen       = 10
)

var (
	ErrTruncated       = errors.New("wire: truncated stream")
	ErrBadMagic        = errors.New("wire: bad magic")
	ErrBadVersion      = errors.New("wire: unsupported version")
	ErrVarintTooLong   = errors.New("wire: varint exceeds ten bytes")
	ErrVarintOverflow  = errors.New("wire: varint overflows uint64")
	ErrUnknownRecord   = errors.New("wire: unknown record tag")
	ErrInvalidLiteral  = errors.New("wire: invalid literal length")
	ErrInvalidMatch    = errors.New("wire: invalid match length or distance")
	ErrLengthMismatch  = errors.New("wire: original length mismatch")
	ErrChecksum        = errors.New("wire: checksum mismatch")
	ErrTrailingBytes   = errors.New("wire: trailing bytes after trailer")
	ErrZeroDistance    = errors.New("wire: match distance is zero")
	ErrDistanceHistory = errors.New("wire: match distance exceeds emitted bytes")
	ErrDistanceWindow  = errors.New("wire: match distance exceeds window")
)

type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return "" }
func (e *OffsetError) Unwrap() error { return nil }

func AppendUvarint(dst []byte, value uint64) []byte { return dst }

func ReadUvarint(src []byte) (uint64, int, error) { return 0, 0, nil }
