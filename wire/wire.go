// Package wire defines the byte format of the compressed stream:
// header, literal runs, back-references, flush marker and the end
// record carrying the original length and checksum. All integers
// are uvarints. It depends on no other package.
package wire

import "errors"

// Stream header: magic "LZ" + version.
const (
	Magic0  = 0x4C // 'L'
	Magic1  = 0x5A // 'Z'
	Version = 0x01

	HeaderLen = 3
)

// Record tags.
const (
	TagLiteral byte = 0x00 // uvarint n + n raw bytes
	TagBackref byte = 0x01 // uvarint dist + uvarint len
	TagFlush   byte = 0x02 // no payload
	TagEnd     byte = 0x03 // uvarint totalLen + uvarint crc32
)

// ErrVarintOverflow reports a uvarint longer than 10 bytes or one
// that overflows 64 bits.
var ErrVarintOverflow = errors.New("wire: uvarint overflow")

// Header returns the 3-byte stream header.
func Header() []byte { return []byte{Magic0, Magic1, Version} }

// AppendUvarint appends v to dst in uvarint form.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// AppendLiteral appends a literal record holding p.
func AppendLiteral(dst, p []byte) []byte {
	if len(p) == 0 {
		return dst
	}
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

// AppendBackref appends a back-reference record.
func AppendBackref(dst []byte, dist, length int) []byte {
	dst = append(dst, TagBackref)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(length))
}

// AppendFlush appends a flush marker.
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

// AppendEnd appends the end record: original length + checksum.
func AppendEnd(dst []byte, total uint64, crc uint32) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, uint64(crc))
}

// Uvarint is an incremental uvarint decoder fed one byte at a time.
type Uvarint struct {
	v uint64
	n int
}

// Reset prepares the decoder for a new value.
func (u *Uvarint) Reset() { u.v, u.n = 0, 0 }

// Feed consumes one byte. done is true when the value is complete.
// It returns ErrVarintOverflow past 10 bytes or on 64-bit overflow.
func (u *Uvarint) Feed(b byte) (done bool, err error) {
	if u.n == 10 || (u.n == 9 && b > 1) {
		return false, ErrVarintOverflow
	}
	u.v |= uint64(b&0x7f) << (7 * u.n)
	u.n++
	if b < 0x80 {
		return true, nil
	}
	return false, nil
}

// Value returns the decoded value; valid once Feed reported done.
func (u *Uvarint) Value() uint64 { return u.v }
