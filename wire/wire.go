// Package wire defines the self-describing byte format of the LZ77 stream.
// It depends on no other package in this module.
package wire

import (
	"errors"
	"fmt"
	"io"
)

// Header is the fixed 3-byte stream prefix.
var Header = []byte{'O', 'Z', 1}

// Record tags.
const (
	TagLiteral  = 0
	TagMatch    = 1
	TagFlush    = 2
	TagEnd      = 3
	MinMatchLen = 3
)

// Sentinel errors; callers use errors.Is to classify failures.
var (
	ErrBadMagic       = errors.New("wire: bad magic number")
	ErrBadVersion     = errors.New("wire: unsupported version")
	ErrZeroDistance   = errors.New("wire: back-reference distance is zero")
	ErrDistOutput     = errors.New("wire: distance exceeds bytes produced")
	ErrDistWindow     = errors.New("wire: distance exceeds window capacity")
	ErrVarintTooLong  = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOverflow = errors.New("wire: varint overflows 64 bits")
	ErrBadTag         = errors.New("wire: unknown record tag")
	ErrBadLength      = errors.New("wire: implausible record length")
	ErrLengthMismatch = errors.New("wire: footer length mismatch")
	ErrChecksum       = errors.New("wire: checksum mismatch")
	ErrTrailingBytes  = errors.New("wire: trailing bytes after stream end")
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrOutputLimit    = errors.New("wire: output size limit exceeded")
)

// FormatError pairs a classified error with the byte offset in the stream.
type FormatError struct {
	Offset int
	Err    error
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("%v at byte offset %d", e.Err, e.Offset)
}
func (e *FormatError) Unwrap() error { return e.Err }

// AppendUvar appends an unsigned LEB128 varint to b.
func AppendUvar(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// WriteUvar writes a varint to w.
func WriteUvar(w io.Writer, v uint64) (int, error) {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	n++
	return w.Write(buf[:n])
}

// WriteLiteral writes tag, length and the literal payload.
func WriteLiteral(w io.Writer, lit []byte) error {
	if _, err := w.Write([]byte{TagLiteral}); err != nil {
		return err
	}
	if _, err := WriteUvar(w, uint64(len(lit))); err != nil {
		return err
	}
	_, err := w.Write(lit)
	return err
}

// WriteMatch writes a back-reference record.
func WriteMatch(w io.Writer, dist, length int) error {
	if _, err := w.Write([]byte{TagMatch}); err != nil {
		return err
	}
	if _, err := WriteUvar(w, uint64(dist)); err != nil {
		return err
	}
	_, err := WriteUvar(w, uint64(length))
	return err
}

// ReadUvar decodes a varint starting at buf[start]. It returns the value, the
// index just past the varint, ErrTruncated when more bytes are needed and
// ErrVarintTooLong/ErrVarintOverflow on malformed encodings.
func ReadUvar(buf []byte, start int) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if start+i >= len(buf) {
			return 0, start, ErrTruncated
		}
		c := buf[start+i]
		if i == 9 && (c&0x7e) != 0 {
			return 0, start, ErrVarintOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return v, start + i + 1, nil
		}
	}
	return 0, start, ErrVarintTooLong
}
