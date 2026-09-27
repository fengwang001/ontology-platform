// Package wire defines the self-describing LZ77 stream byte format.
// It depends on no other package of this module.
package wire

import "encoding/binary"

// Magic is the 3-byte stream header followed by Version.
var Magic = [3]byte{'L', 'Z', '7'}

const Version byte = 1

// Record tags (each encoded as one varint).
const (
	TagLit byte = 0 // literal run: varint n, then n raw bytes
	TagRef byte = 1 // back reference: varint distance, varint length
	TagFlush byte = 2 // flush boundary: no payload
	TagEnd byte = 3 // stream trailer: varint total, varint crc64
)

// MaxVarintLen is the maximum legal number of bytes for a 64-bit varint.
const MaxVarintLen = binary.MaxVarintLen64 // 10

// AppendUvarint appends an unsigned LEB128 varint.
func AppendUvarint(b []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(b, buf[:n]...)
}

// ReadUvarint decodes one varint at b[0:]. It returns the value, the number
// of bytes consumed, and false when more input is required.
func ReadUvarint(b []byte) (uint64, int, bool) {
	v, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, 0, false
	}
	return v, n, true
}

// VarintOverflow reports whether b holds exactly 10 continuation bytes with no
// terminator at the 10th position, i.e. a 64-bit-overflowing/over-long varint.
func VarintOverflow(b []byte) bool {
	if len(b) < MaxVarintLen {
		return false
	}
	for i := 0; i < MaxVarintLen-1; i++ {
		if b[i]&0x80 == 0 {
			return false
		}
	}
	return b[MaxVarintLen-1]&0x80 != 0
}

// Header returns the encoded stream header.
func Header() []byte { return []byte{Magic[0], Magic[1], Magic[2], Version} }

// Literal appends a literal record.
func Literal(b, data []byte) []byte {
	b = AppendUvarint(b, uint64(TagLit))
	b = AppendUvarint(b, uint64(len(data)))
	return append(b, data...)
}

// Reference appends a back-reference record.
func Reference(b []byte, dist, length int) []byte {
	b = AppendUvarint(b, uint64(TagRef))
	b = AppendUvarint(b, uint64(dist))
	b = AppendUvarint(b, uint64(length))
	return b
}

// Flush appends a flush-boundary record.
func Flush(b []byte) []byte { return AppendUvarint(b, uint64(TagFlush)) }

// Trailer appends the stream trailer (original length + checksum).
func Trailer(b []byte, total uint64, checksum uint64) []byte {
	b = AppendUvarint(b, uint64(TagEnd))
	b = AppendUvarint(b, total)
	b = AppendUvarint(b, checksum)
	return b
}
