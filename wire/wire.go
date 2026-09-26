// Package wire defines the self-describing LZ77 stream byte format.
package wire

import "errors"

const (
	TagLit   = 0
	TagMatch = 1
	TagFlush = 2
	TagEnd   = 3
)

const MaxVarintLen = 10

var Header = [4]byte{'O', 'L', 'Z', 0x01}

// ErrVarint marks a varint longer than 10 bytes or overflowing uint64.
var ErrVarint = errors.New("wire: varint overflow or too long")

func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint returns the value, bytes consumed, and whether a full varint is present.
// ok=false with err!=nil means a definite malformed varint; err==nil means truncated.
func ReadUvarint(src []byte) (v uint64, n int, ok bool, err error) {
	var shift uint
	for n = 0; n < len(src) && n < MaxVarintLen; n++ {
		b := src[n]
		if n == MaxVarintLen-1 && (b > 1 || b&0x80 != 0) {
			return 0, n + 1, false, ErrVarint
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, n + 1, true, nil
		}
		shift += 7
	}
	if len(src) >= MaxVarintLen {
		return 0, MaxVarintLen, false, ErrVarint
	}
	return 0, len(src), false, nil
}

// Hasher is an incremental FNV-1a 64-bit checksum.
type Hasher struct{ h uint64 }

func NewHasher() *Hasher { return &Hasher{h: 14695981039346656037} }

func (x *Hasher) Write(p []byte) {
	for _, b := range p {
		x.h ^= uint64(b)
		x.h *= 1099511628211
	}
}

func (x *Hasher) Sum() uint64 { return x.h }

func Checksum(b []byte) uint64 {
	h := NewHasher()
	h.Write(b)
	return h.Sum()
}
