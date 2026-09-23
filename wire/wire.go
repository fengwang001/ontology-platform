package wire

import (
	"errors"
	"fmt"
)

// Tag identifies one stream record.
type Tag byte

const (
	TagHeader Tag = 1 + iota
	TagLiteral
	TagMatch
	TagFlush
	TagEnd
)

const (
	// Version is the only accepted wire version.
	Version = 1
	// Magic makes an empty/foreign byte stream detectable immediately.
	Magic        = uint32(0x4c5a3731)
	MaxVarintLen = 10
)

var (
	ErrBadMagic       = errors.New("wire: bad magic")
	ErrBadVersion     = errors.New("wire: unsupported version")
	ErrVarintTooLong  = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOverflow = errors.New("wire: varint overflows uint64")
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrZeroDistance   = errors.New("wire: back-reference distance is zero")
	ErrDistancePast   = errors.New("wire: distance exceeds produced output")
	ErrWindowOverflow = errors.New("wire: distance exceeds window capacity")
	ErrLengthMismatch = errors.New("wire: declared total length mismatch")
	ErrChecksum       = errors.New("wire: checksum mismatch")
	ErrTrailingBytes  = errors.New("wire: bytes after stream end")
	ErrOutputLimit    = errors.New("wire: configured output limit exceeded")
)

// OffsetError attains the byte offset at which malformed data was detected.
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return fmt.Sprintf("%v at byte %d", e.Err, e.Offset) }
func (e *OffsetError) Unwrap() error { return e.Err }

// At wraps err with stream offset off.
func At(off int, err error) error {
	if err == nil {
		return nil
	}
	return &OffsetError{Offset: off, Err: err}
}

// AppendUvarint appends an unsigned LEB128 integer.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint consumes an integer from src beginning at index off.
// It returns the value, bytes consumed, and a sentinel classification.
func ReadUvarint(src []byte, off int) (uint64, int, error) {
	var x uint64
	start := off
	for shift := uint(0); shift < 64; shift += 7 {
		if off >= len(src) {
			return 0, off - start, ErrTruncated
		}
		b := src[off]
		off++
		if off-start > MaxVarintLen {
			return 0, off - start, ErrVarintTooLong
		}
		if shift == 63 && b > 1 {
			return 0, off - start, ErrVarintOverflow
		}
		x |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return x, off - start, nil
		}
	}
	return 0, off - start, ErrVarintTooLong
}

// Config selects implementation limits shared by encoder and decoder.
type Config struct {
	WindowCap int
	MaxChain  int
	MaxOutput int64
}

// HeaderBytes is the canonical complete stream header.
func HeaderBytes(windowCap int) []byte {
	b := []byte{byte(TagHeader)}
	b = AppendUvarint(b, uint64(Magic))
	b = AppendUvarint(b, Version)
	b = AppendUvarint(b, uint64(windowCap))
	return b
}
