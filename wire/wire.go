package wire

import "errors"

// Tag identifies a stream record.
type Tag uint8

const (
	TagLiteral Tag = 0
	TagMatch   Tag = 1
	TagFlush   Tag = 2
	TagEnd     Tag = 3
)

// Magic is the fixed stream header, version included.
var Magic = []byte{'L', 'Z', '7', '7', 1}

// ErrVarint means a varint exceeds 10 bytes or overflows uint64.
var ErrVarint = errors.New("wire: varint too long or overflow")

// AppendUvarint appends an unsigned LEB128 integer.
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint consumes one varint from b. n is bytes consumed.
func ReadUvarint(b []byte) (v uint64, n int, err error) {
	var x uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 { // 10th byte holds at most 1 bit.
			return 0, 0, ErrVarint
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, ErrVarint
}

// AppendHeader appends the stream header.
func AppendHeader(b []byte) []byte { return append(b, Magic...) }

// AppendTag appends a record tag.
func AppendTag(b []byte, t Tag) []byte { return AppendUvarint(b, uint64(t)) }

// AppendLiteral appends a literal run.
func AppendLiteral(b, p []byte) []byte {
	b = AppendTag(b, TagLiteral)
	b = AppendUvarint(b, uint64(len(p)))
	return append(b, p...)
}

// AppendMatch appends a back-reference (distance, length), min match 3.
func AppendMatch(b []byte, dist, length int) []byte {
	b = AppendTag(b, TagMatch)
	b = AppendUvarint(b, uint64(dist-1))
	b = AppendUvarint(b, uint64(length-3))
	return b
}

// AppendFlush appends a flush marker.
func AppendFlush(b []byte) []byte { return AppendTag(b, TagFlush) }

// AppendEnd appends the stream end with total length and checksum.
func AppendEnd(b []byte, total uint64, crc uint32) []byte {
	b = AppendTag(b, TagEnd)
	b = AppendUvarint(b, total)
	return AppendUvarint(b, uint64(crc))
}
