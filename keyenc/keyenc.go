// Package keyenc defines the old (E1) and new (E2) key encodings and maps
// logical key intervals to physical encoded-byte intervals.
package keyenc

import "encoding/binary"

// Key bounds.
const (
	MinKey = int64(-1_000_000_000_000)
	MaxKey = int64(1_000_000_000_000)
)

// KeyLen is the fixed encoded key length in bytes.
const KeyLen = 8

// Range is a physical half-open interval [Low, High). A nil High means +inf.
type Range struct {
	Low  []byte
	High []byte
}

// Encode1 returns E1(k): the 64-bit two's-complement big-endian encoding.
func Encode1(k int64) []byte {
	b := make([]byte, KeyLen)
	binary.BigEndian.PutUint64(b, uint64(k))
	return b
}

// Encode2 returns E2(k): E1(k) with the most significant bit flipped, which
// makes the unsigned byte order coincide with signed logical key order.
func Encode2(k int64) []byte {
	b := Encode1(k)
	b[0] ^= 0x80
	return b
}

// Decode1 decodes an E1-encoded key.
func Decode1(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b))
}

// Decode2 decodes an E2-encoded key.
func Decode2(b []byte) int64 {
	c := make([]byte, KeyLen)
	copy(c, b)
	c[0] ^= 0x80
	return Decode1(c)
}

// E1Ranges maps the logical interval [lo, hi) to physical E1 ranges in
// logical ascending scan order: the negative half ([a,0) is stored after all
// non-negative keys) first, then the non-negative half. Empty intervals
// produce no ranges. Callers pass validated bounds within [MinKey, MaxKey+1].
func E1Ranges(lo, hi int64) []Range {
	if lo >= hi {
		return nil
	}
	ranges := make([]Range, 0, 2)
	if lo < 0 {
		negHi := hi
		if negHi > 0 {
			negHi = 0
		}
		if lo < negHi {
			r := Range{Low: Encode1(lo)}
			if negHi == 0 {
				// The negative half reaches the physical end. The bound must
				// be genuine +inf (nil): E1(0) is the all-zero physical
				// minimum, and even 0xff..ff is a valid negative key (-1).
				r.High = nil
			} else {
				r.High = Encode1(negHi)
			}
			ranges = append(ranges, r)
		}
	}
	if hi > 0 {
		posLo := lo
		if posLo < 0 {
			posLo = 0
		}
		if posLo < hi {
			ranges = append(ranges, Range{Low: Encode1(posLo), High: Encode1(hi)})
		}
	}
	return ranges
}

// E2Ranges maps the logical interval [lo, hi) to physical E2 ranges. E2 is
// order preserving, so a non-empty interval maps to exactly one range.
func E2Ranges(lo, hi int64) []Range {
	if lo >= hi {
		return nil
	}
	return []Range{{Low: Encode2(lo), High: Encode2(hi)}}
}
