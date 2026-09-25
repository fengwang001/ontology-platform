// Package wire defines the self-describing LZ77 stream byte format.
// It has no dependencies on the other packages in this module.
package wire

import "errors"

// Sentinel errors. Callers use errors.Is to classify corrupt streams.
var (
	ErrBadMagic       = errors.New("wire: bad stream magic")
	ErrBadVersion     = errors.New("wire: unsupported stream version")
	ErrZeroDistance   = errors.New("wire: match distance is zero")
	ErrDistBeyondHist = errors.New("wire: match distance beyond emitted history")
	ErrDistBeyondWin  = errors.New("wire: match distance beyond window capacity")
	ErrVarint         = errors.New("wire: varint too long or overflows uint64")
	ErrLengthMismatch = errors.New("wire: end-record length mismatch")
	ErrChecksum       = errors.New("wire: checksum mismatch")
	ErrTrailing       = errors.New("wire: trailing bytes after end record")
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrOutputLimit    = errors.New("wire: decompressed output exceeds limit")
	ErrBadConfig      = errors.New("wire: invalid configuration")
	ErrBadRecord      = errors.New("wire: unknown or malformed record")
)

const (
	Magic0 byte = 'L'
	Magic1 byte = 'Z'
	Magic2 byte = '7'
	Magic3 byte = 'O'

	Version uint64 = 1

	TagLiteral byte = 1
	TagMatch   byte = 2
	TagFlush   byte = 3
	TagEnd     byte = 4
)

// AppendUvarint appends an unsigned LEB128 varint.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint decodes one varint. It returns the value, bytes consumed and an
// error. ErrTruncated means more bytes are needed; ErrVarint means the encoding
// exceeds 10 bytes or overflows 64 bits.
func ReadUvarint(src []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(src) && i < 10; i++ {
		b := src[i]
		if i == 9 && b > 1 { // 10th byte may carry at most 1 bit for uint64.
			return 0, 0, ErrVarint
		}
		x |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return x, i + 1, nil
		}
	}
	if len(src) >= 10 {
		return 0, 0, ErrVarint
	}
	return 0, 0, ErrTruncated
}

// AppendHeader appends the fixed magic followed by the version varint.
func AppendHeader(dst []byte) []byte {
	dst = append(dst, Magic0, Magic1, Magic2, Magic3)
	return AppendUvarint(dst, Version)
}

// AppendLiteral appends a literal run (len(p) >= 1).
func AppendLiteral(dst, p []byte) []byte {
	dst = AppendUvarint(dst, uint64(TagLiteral))
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

// AppendMatch appends a back-reference (distance >= 1, length >= MinMatch).
func AppendMatch(dst []byte, distance, length int) []byte {
	dst = AppendUvarint(dst, uint64(TagMatch))
	dst = AppendUvarint(dst, uint64(distance))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush appends the flush marker.
func AppendFlush(dst []byte) []byte {
	return AppendUvarint(dst, uint64(TagFlush))
}

// AppendEnd appends the stream trailer with total length and checksum.
func AppendEnd(dst []byte, totalLength, checksum uint64) []byte {
	dst = AppendUvarint(dst, uint64(TagEnd))
	dst = AppendUvarint(dst, totalLength)
	return AppendUvarint(dst, checksum)
}

// ReadTag reads one tag varint.
func ReadTag(src []byte) (byte, int, error) {
	v, n, err := ReadUvarint(src)
	if err != nil {
		return 0, 0, err
	}
	return byte(v), n, nil
}
