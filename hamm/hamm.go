// Package hamm implements the Hamming(7,4) single-error-correcting code:
// parity-bit generation, syndrome computation and single-error correction.
// It is pure bit arithmetic and depends on no other package.
package hamm

// EncodeNibble encodes a 4-bit nibble (0..15) into a 7-bit codeword.
// The codeword occupies bits 6..0 of the result; bit (7-pos) is codeword
// position pos, laid out as p1 p2 d1 p3 d2 d3 d4 with even parity:
// p1 = d1^d2^d4, p2 = d1^d3^d4, p3 = d2^d3^d4.
// The caller must guarantee 0 <= n <= 15.
func EncodeNibble(n int) int {
	d1 := n >> 3 & 1
	d2 := n >> 2 & 1
	d3 := n >> 1 & 1
	d4 := n & 1
	p1 := d1 ^ d2 ^ d4
	p2 := d1 ^ d3 ^ d4
	p3 := d2 ^ d3 ^ d4
	return p1<<6 | p2<<5 | d1<<4 | p3<<3 | d2<<2 | d3<<1 | d4
}

// DecodeNibble corrects a single-bit error in the 7-bit codeword cw
// (bits 6..0, same layout as EncodeNibble) and returns the 4 data bits
// as a nibble d1*8+d2*4+d3*2+d4. The syndrome s1|s2<<1|s3<<2 directly
// gives the 1-based position of the erroneous bit (0 = no error).
func DecodeNibble(cw int) int {
	bit := func(pos int) int { return cw >> (7 - pos) & 1 }
	s1 := bit(1) ^ bit(3) ^ bit(5) ^ bit(7) // p1^d1^d2^d4
	s2 := bit(2) ^ bit(3) ^ bit(6) ^ bit(7) // p2^d1^d3^d4
	s3 := bit(4) ^ bit(5) ^ bit(6) ^ bit(7) // p3^d2^d3^d4
	if syn := s1 | s2<<1 | s3<<2; syn != 0 {
		cw ^= 1 << (7 - syn) // flip the erroneous bit back
	}
	return bit(3)<<3 | bit(5)<<2 | bit(6)<<1 | bit(7)
}
