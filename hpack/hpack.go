// Package hpack packs 7-bit Hamming codewords into a big-endian bit
// stream of bytes and unpacks them back, locating codeword i at bit
// offset 7*i. It depends only on hamm.
package hpack

import "ontology/hamm"

// Pack encodes each nibble with hamm.EncodeNibble and appends its 7-bit
// codeword to a big-endian bit stream; the last byte is zero-padded in
// its low bits. The caller must guarantee every nibble is in [0,15].
func Pack(ns []int) []byte {
	out := make([]byte, 0, (7*len(ns)+7)/8)
	var acc uint64
	nbits := 0
	for _, n := range ns {
		acc = acc<<7 | uint64(hamm.EncodeNibble(n))
		nbits += 7
		for nbits >= 8 {
			nbits -= 8
			out = append(out, byte(acc>>nbits))
		}
		acc &= 1<<uint(nbits) - 1 // drop emitted bits, keep only the remainder
	}
	if nbits > 0 {
		out = append(out, byte(acc<<uint(8-nbits)))
	}
	return out
}

// Unpacker extracts codewords from a byte stream. The start of codeword
// i is computed directly as bit offset 7*i (O(1)); locateScans records
// how many bits were scanned to locate the most recent codeword start —
// always 0 here, proving no walk from the stream head. It is unexported
// and never appears in the public API.
type Unpacker struct {
	locateScans int
}

// Unpack decodes n codewords from b and returns their data nibbles.
// The caller must guarantee 8*len(b) >= 7*n.
func (u *Unpacker) Unpack(b []byte, n int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		u.locateScans = 0 // direct offset 7*i: no bits scanned to locate
		cw := 0
		for j := 0; j < 7; j++ {
			pos := 7*i + j
			cw = cw<<1 | int(b[pos/8]>>(7-uint(pos%8)))&1
		}
		out = append(out, hamm.DecodeNibble(cw))
	}
	return out
}
