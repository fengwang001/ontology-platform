// Package wire defines the byte format of the custom LZ77 stream:
// header, literal runs, back-references, flush marks and the trailer.
// All integers are uvarint encoded. It depends on no other package.
package wire

import "encoding/binary"

// Stream header: 3 magic bytes + 1 version byte.
const (
	Magic0   = 'O'
	Magic1   = 'L'
	Magic2   = 'Z'
	Version  = 1
	HeaderLn = 4
)

// Record tags.
const (
	TagLiteral = 0x01
	TagMatch   = 0x02
	TagFlush   = 0x03
	TagEnd     = 0x04
)

// Header returns the 4-byte stream header.
func Header() []byte { return []byte{Magic0, Magic1, Magic2, Version} }

// AppendUvarint appends v uvarint-encoded to dst.
func AppendUvarint(dst []byte, v uint64) []byte {
	return binary.AppendUvarint(dst, v)
}

// AppendLiteral appends a literal record holding payload.
func AppendLiteral(dst, payload []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}

// AppendMatch appends a back-reference record (dist >= 1, len >= 1).
func AppendMatch(dst []byte, dist, length int) []byte {
	dst = append(dst, TagMatch)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush appends a flush mark.
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

// AppendEnd appends the trailer: total input length and its crc32.
func AppendEnd(dst []byte, total, crc uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, crc)
}

// ReadUvarint decodes a uvarint from b. n == 0 means b is too short
// (more input needed); overflow reports > 10 bytes or 64-bit overflow.
func ReadUvarint(b []byte) (v uint64, n int, overflow bool) {
	v, n = binary.Uvarint(b)
	if n < 0 {
		return 0, -n, true
	}
	return v, n, false
}
