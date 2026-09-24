// Package b32 implements base32 encoding and decoding over 5-byte groups.
package b32

import (
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/alpha"
)

// Sentinel errors distinguishing the three rejection causes.
var (
	ErrInvalidChar     = errors.New("b32: character not in alphabet")
	ErrPaddingPosition = errors.New("b32: padding in non-final position")
	ErrPaddingCount    = errors.New("b32: illegal padding count")
)

// legalPad is the set of padding counts derived in NOTES.md: {0,1,3,4,6}.
var legalPad = [8]bool{0: true, 1: true, 3: true, 4: true, 6: true}

// Codec encodes and decodes base32. It is safe for concurrent use.
type Codec struct {
	groups atomic.Int64 // 5-byte groups processed; never exposed directly
}

func New() *Codec { return &Codec{} }

func (c *Codec) Groups() int64 { return c.groups.Load() }

// Encode returns the base32 encoding of src, one 8-char group per 5 bytes.
func (c *Codec) Encode(src []byte) string {
	out := make([]byte, 0, (len(src)+4)/5*8)
	for len(src) > 0 {
		n := min(len(src), 5)
		out = append(out, encodeGroup(src[:n])...)
		c.groups.Add(1)
		src = src[n:]
	}
	return string(out)
}

// encodeGroup maps 1..5 bytes to exactly 8 characters per the padding table.
func encodeGroup(b []byte) string {
	var buf [5]byte
	copy(buf[:], b)
	v := [8]byte{
		buf[0] >> 3, (buf[0]<<2 | buf[1]>>6) & 31,
		(buf[1] >> 1) & 31, (buf[1]<<4 | buf[2]>>4) & 31,
		(buf[2]<<1 | buf[3]>>7) & 31, (buf[3] >> 2) & 31,
		(buf[3]<<3 | buf[4]>>5) & 31, buf[4] & 31,
	}
	chars := (len(b)*8 + 4) / 5 // ceil(8n/5): 2,4,5,7,8 for n=1..5
	var out [8]byte
	for i := range out {
		if i < chars {
			out[i] = alpha.Char(v[i])
		} else {
			out[i] = alpha.Padding
		}
	}
	return string(out[:])
}

// Decode reverses Encode; rejections return nil plus a sentinel error.
func (c *Codec) Decode(s string) ([]byte, error) {
	if len(s)%8 != 0 {
		return nil, fmt.Errorf("%w: length %d not a multiple of 8", ErrPaddingCount, len(s))
	}
	out := make([]byte, 0, len(s)/8*5)
	for i := 0; i < len(s); i += 8 {
		blk := s[i : i+8]
		pad := 0
		for j := 7; j >= 0 && blk[j] == alpha.Padding; j-- {
			pad++
		}
		if !legalPad[pad] {
			return nil, fmt.Errorf("%w: %d at char %d", ErrPaddingCount, pad, i)
		}
		var acc uint64
		bits := 0
		for j := 0; j < 8-pad; j++ {
			if blk[j] == alpha.Padding {
				return nil, fmt.Errorf("%w: at char %d", ErrPaddingPosition, i+j)
			}
			v, ok := alpha.Value(blk[j])
			if !ok {
				return nil, fmt.Errorf("%w: %q at char %d", ErrInvalidChar, blk[j], i+j)
			}
			acc, bits = acc<<5|uint64(v), bits+5
			if bits >= 8 {
				bits -= 8
				out = append(out, byte(acc>>bits))
				acc &= 1<<bits - 1
			}
		}
		c.groups.Add(1)
	}
	return out, nil
}
