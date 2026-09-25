// Package hamm implements Hamming(7,4) parity generation, syndrome
// computation and single-error correction for one nibble. Pure bit ops.
package hamm

import "errors"

// ErrInvalidNibble is returned when a nibble is outside 0..15.
var ErrInvalidNibble = errors.New("hamm: nibble out of range 0..15")

// bit returns the bit at 1-based codeword position pos (position 1 is the
// most significant of the 7-bit word packed in the low bits of w).
func bit(w uint8, pos int) uint8 { return w >> (7 - pos) & 1 }

// EncodeNibble maps a nibble 0..15 to its 7-bit codeword, returned in the
// low 7 bits with position 1 (p1) as the most significant bit.
// Layout: p1 p2 d1 p3 d2 d3 d4, even parity.
func EncodeNibble(n int) (uint8, error) {
	if n < 0 || n > 15 {
		return 0, ErrInvalidNibble
	}
	d1 := uint8(n >> 3 & 1)
	d2 := uint8(n >> 2 & 1)
	d3 := uint8(n >> 1 & 1)
	d4 := uint8(n & 1)
	p1 := d1 ^ d2 ^ d4
	p2 := d1 ^ d3 ^ d4
	p3 := d2 ^ d3 ^ d4
	return p1<<6 | p2<<5 | d1<<4 | p3<<3 | d2<<2 | d3<<1 | d4, nil
}

// DecodeNibble corrects a single-bit error in the 7-bit codeword w (low 7
// bits, position 1 most significant) and returns the data nibble 0..15.
// A non-zero syndrome names the 1-based position to flip.
func DecodeNibble(w uint8) int {
	w &= 0x7f
	p1, p2, p3 := bit(w, 1), bit(w, 2), bit(w, 4)
	d1, d2, d3, d4 := bit(w, 3), bit(w, 5), bit(w, 6), bit(w, 7)
	s1 := p1 ^ d1 ^ d2 ^ d4
	s2 := p2 ^ d1 ^ d3 ^ d4
	s3 := p3 ^ d2 ^ d3 ^ d4
	syndrome := int(s1)*1 + int(s2)*2 + int(s3)*4
	if syndrome != 0 {
		w ^= 1 << (7 - syndrome)
	}
	return int(bit(w, 3))<<3 | int(bit(w, 5))<<2 | int(bit(w, 6))<<1 | int(bit(w, 7))
}
