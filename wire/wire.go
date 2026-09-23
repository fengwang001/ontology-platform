// Package wire defines the self-describing LZ77 compressed stream byte format.
//
// A stream is: header, then zero or more records, then a trailer.
// All integers use unsigned LEB128 varints limited to 10 bytes.
package wire

import "errors"

const (
	TagLiteral byte = 0 // varint n, then n literal bytes
	TagMatch   byte = 1 // varint(distance-1), varint(length-3)
	TagFlush   byte = 2 // no payload
	TagEnd     byte = 3 // varint original length, varint FNV-1a 32 checksum
)

const (
	MaxVarintLen = 10
	MaxMatchLen  = 1 << 16
	MinMatchLen  = 3
)

// Magic is the fixed stream header prefix followed by version byte 1 and
// varint window capacity.
var Magic = []byte{'L', 'Z', '7', 1}

// ErrVarintTooLong means a varint exceeds 10 bytes or overflows uint64.
var ErrVarintTooLong = errors.New("wire: varint too long")

// AppendVarint appends v as an unsigned LEB128 varint.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadVarint decodes an unsigned LEB128 varint from b. It returns the value,
// bytes consumed, and an error. n>0 with nil error on success; n==0 with nil
// error means no byte is available yet. An unfinished varint at end of input
// is indistinguishable here from "need more bytes" and is finalized as
// truncation by the streaming decoder on Close.
func ReadVarint(b []byte) (v uint64, n int, err error) {
	for shift := uint(0); shift < 64; shift += 7 {
		if n >= len(b) {
			return 0, n, nil
		}
		c := b[n]
		n++
		if n == MaxVarintLen && c&0x7f > 1 {
			return 0, n, ErrVarintTooLong
		}
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, n, nil
		}
		if n == MaxVarintLen {
			return 0, n, ErrVarintTooLong
		}
	}
	return 0, n, ErrVarintTooLong
}

// Checksum returns the 32-bit FNV-1a hash of p.
func Checksum(p []byte) uint32 {
	const (
		offset32 = uint32(2166136261)
		prime32  = uint32(16777619)
	)
	h := offset32
	for _, c := range p {
		h ^= uint32(c)
		h *= prime32
	}
	return h
}
