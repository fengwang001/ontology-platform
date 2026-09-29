// Package wire defines the compressed stream byte format.
package wire

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3

	Version = 1
)

// MaxVarintLen is the maximum legal length of a base-128 varint.
const MaxVarintLen = 10

// Magic is the 3-byte stream header magic.
var Magic = [3]byte{'O', 'L', 'Z'}

// PutVarint appends an unsigned base-128 varint to b.
func PutVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadVarint decodes one varint, returning value, bytes consumed, ok.
// ok is false when truncated; overflow is reported separately via overflow.
func ReadVarint(b []byte) (v uint64, n int, ok bool) {
	var shift uint
	for n = 0; n < MaxVarintLen && n < len(b); n++ {
		c := b[n]
		if n == MaxVarintLen-1 && (c&0x80 != 0 || c > 1) {
			return 0, n + 1, false
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, n + 1, true
		}
		shift += 7
	}
	return 0, n, false
}

// AppendHeader/End/Flush build fixed records.
func AppendHeader(b []byte) []byte {
	return append(b, Magic[0], Magic[1], Magic[2], Version)
}

// AppendLiteral appends a literal record.
func AppendLiteral(b []byte, p []byte) []byte {
	b = append(b, TagLiteral)
	b = PutVarint(b, uint64(len(p)))
	return append(b, p...)
}

// AppendMatch appends a back-reference record.
func AppendMatch(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = PutVarint(b, uint64(dist))
	b = PutVarint(b, uint64(length))
	return b
}

func AppendFlush(b []byte) []byte { return append(b, TagFlush) }
func AppendEnd(b []byte, n int, sum uint64) []byte {
	b = append(b, TagEnd)
	b = PutVarint(b, uint64(n))
	b = PutVarint(b, sum)
	return b
}

// FNV-1a 64 over original bytes.
func Checksum(seed uint64, p []byte) uint64 {
	h := seed
	for _, c := range p {
		h ^= uint64(c)
		h *= 0x100000001b3
	}
	return h
}

const ChecksumSeed = 0xcbf29ce484222325
