// Package wire defines the self-described LZ77 byte stream format.
package wire

const (
	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

// Header is the fixed 5-byte stream prefix.
var Header = []byte{'L', 'Z', '7', '7', 0x01}

// AppendVarint appends an unsigned LEB128 integer.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// VarintLen returns the encoded length of v (1..10 bytes).
func VarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

var (
	ErrVarintTruncated = wireError("wire: varint truncated")
	ErrVarintTooLong   = wireError("wire: varint longer than 10 bytes")
	ErrVarintOverflow  = wireError("wire: varint overflows 64 bits")
)

type wireError string

func (e wireError) Error() string { return string(e) }

// ReadVarint decodes one varint from the front of p, returning the value and
// the number of bytes consumed. A nil remainder signals a truncated record.
func ReadVarint(p []byte) (v uint64, n int, rest []byte, err error) {
	for shift := uint(0); shift < 64; shift += 7 {
		if n >= len(p) {
			return 0, n, nil, ErrVarintTruncated
		}
		b := p[n]
		n++
		if n == 10 {
			switch {
			case b == 0x80, b == 0x81:
				return 0, n, nil, ErrVarintTooLong
			case b > 1:
				return 0, n, nil, ErrVarintOverflow
			}
		}
		if n > 10 {
			return 0, n, nil, ErrVarintTooLong
		}
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, n, p[n:], nil
		}
	}
	return 0, n, nil, ErrVarintOverflow
}

// FNV-1a 64, incrementally maintained by compressor and decompressor.
const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Checksum folds bytes into a running FNV-1a 64 checksum.
func Checksum(h uint64, p []byte) uint64 {
	for _, b := range p {
		h ^= uint64(b)
		h *= fnvPrime
	}
	return h
}

// InitialChecksum is the FNV-1a 64 initial value.
const InitialChecksum = fnvOffset
