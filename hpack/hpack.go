// Package hpack packs 7-bit Hamming codewords into a big-endian bit
// stream and unpacks them back, locating codeword i at bit offset 7*i.
package hpack

import "errors"

var (
	// ErrTruncated: bit stream holds fewer than 7*n bits.
	ErrTruncated = errors.New("hpack: bit stream too short for n codewords")
	// ErrIllegalPadding: bits beyond 7*n are not all zero.
	ErrIllegalPadding = errors.New("hpack: non-zero padding bits")
)

func bitAt(b []byte, pos int) uint8 { return b[pos/8] >> (7 - pos%8) & 1 }

// Pack concatenates 7-bit codewords (low 7 bits each, position 1 first)
// into a big-endian byte stream; the final byte is zero-padded.
func Pack(words []uint8) []byte {
	out := make([]byte, (7*len(words)+7)/8)
	pos := 0
	for _, w := range words {
		for i := 6; i >= 0; i-- {
			if w>>i&1 == 1 {
				out[pos/8] |= 1 << (7 - pos%8)
			}
			pos++
		}
	}
	return out
}

// Unpacker extracts codewords from a bit stream. scanned is an unexported
// counter recording how many bits were scanned to locate the start of the
// most recent codeword; direct 7*i offset computation keeps it at zero.
type Unpacker struct{ scanned int }

// start returns the bit offset of codeword i in O(1).
func (u *Unpacker) start(i int) int {
	u.scanned = 0
	return 7 * i
}

// Unpack returns the first n 7-bit codewords of b. It fails wholesale
// (nil result) on truncation or non-zero padding.
func (u *Unpacker) Unpack(b []byte, n int) ([]uint8, error) {
	if 8*len(b) < 7*n {
		return nil, ErrTruncated
	}
	for pos := 7 * n; pos < 8*len(b); pos++ {
		if bitAt(b, pos) == 1 {
			return nil, ErrIllegalPadding
		}
	}
	out := make([]uint8, n)
	for i := 0; i < n; i++ {
		pos := u.start(i)
		var w uint8
		for k := 0; k < 7; k++ {
			w = w<<1 | bitAt(b, pos+k)
		}
		out[i] = w
	}
	return out, nil
}

// Unpack is the function form of Unpacker.Unpack.
func Unpack(b []byte, n int) ([]uint8, error) { return new(Unpacker).Unpack(b, n) }

// Feeder accumulates byte chunks; chunk boundaries never affect the
// resulting stream.
type Feeder struct{ buf []byte }

// Feed appends one chunk.
func (f *Feeder) Feed(p []byte) { f.buf = append(f.buf, p...) }

// Bytes returns the accumulated stream.
func (f *Feeder) Bytes() []byte { return f.buf }
