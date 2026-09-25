// Package wire defines the on-the-wire byte format of the LZ77 stream.
package wire

import "encoding/binary"

// Magic identifies the stream. Version is the only supported version.
const (
	Magic   = "LZ71"
	Version = uint64(1)
)

// Record tags.
const (
	TagLit   = uint64(0)
	TagMatch = uint64(1)
	TagFlush = uint64(2)
	TagEnd   = uint64(3)
)

// MaxVarintLen is the length of a maximal unsigned 64-bit varint.
const MaxVarintLen = binary.MaxVarintLen64 // 10

// AppendUvarint appends x as a little-endian unsigned varint.
func AppendUvarint(buf []byte, x uint64) []byte {
	var tmp [MaxVarintLen]byte
	n := binary.PutUvarint(tmp[:], x)
	return append(buf, tmp[:n]...)
}

// ReadUvarint decodes one varint from b.
// n==0 means the record is truncated; n<0 means the encoding overflows 64 bits
// or exceeds MaxVarintLen bytes; otherwise n is the number of bytes consumed.
func ReadUvarint(b []byte) (x uint64, n int) {
	x, n = binary.Uvarint(b)
	switch {
	case n > 0:
		return x, n
	case n == 0:
		return 0, 0
	default:
		return 0, -1
	}
}

// Header returns the encoded stream header for the given window capacity.
func Header(winCap uint64) []byte {
	buf := make([]byte, 0, len(Magic)+2*MaxVarintLen)
	buf = append(buf, Magic...)
	buf = AppendUvarint(buf, Version)
	buf = AppendUvarint(buf, winCap)
	return buf
}

// Literal appends a literal record carrying p.
func Literal(buf []byte, p []byte) []byte {
	buf = AppendUvarint(buf, TagLit)
	buf = AppendUvarint(buf, uint64(len(p))-1)
	return append(buf, p...)
}

// Match appends a back-reference record. distance and length are >=1.
func Match(buf []byte, distance, length int) []byte {
	buf = AppendUvarint(buf, TagMatch)
	buf = AppendUvarint(buf, uint64(distance)-1)
	buf = AppendUvarint(buf, uint64(length)-1)
	return buf
}

// Flush appends a flush marker.
func Flush(buf []byte) []byte { return AppendUvarint(buf, TagFlush) }

// End appends the stream tail with the original length and CRC-32 (IEEE).
func End(buf []byte, total, crc uint64) []byte {
	buf = AppendUvarint(buf, TagEnd)
	buf = AppendUvarint(buf, total)
	buf = AppendUvarint(buf, crc)
	return buf
}
