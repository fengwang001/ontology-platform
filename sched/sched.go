// Package sched holds the SHA-256 message-schedule primitives: the small and
// big sigma functions, Ch/Maj, the round constants K and the initial vector
// IV. It depends on no other package in this module.
package sched

import "encoding/binary"

// IV is the SHA-256 initial vector, in a..h order.
var IV = [8]uint32{
	0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
	0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
}

// K holds the 64 round constants K[0..63].
var K = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
	0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
	0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
	0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
	0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
	0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
	0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
	0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
	0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

func rotr(x uint32, n uint) uint32 { return x>>n | x<<(32-n) }

// SmallSigma0 is σ0(x) = ROTR^7(x) ⊕ ROTR^18(x) ⊕ SHR^3(x) (logical shift).
func SmallSigma0(x uint32) uint32 { return rotr(x, 7) ^ rotr(x, 18) ^ x>>3 }

// SmallSigma1 is σ1(x) = ROTR^17(x) ⊕ ROTR^19(x) ⊕ SHR^10(x) (logical shift).
func SmallSigma1(x uint32) uint32 { return rotr(x, 17) ^ rotr(x, 19) ^ x>>10 }

// BigSigma0 is Σ0(x) = ROTR^2(x) ⊕ ROTR^13(x) ⊕ ROTR^22(x).
func BigSigma0(x uint32) uint32 { return rotr(x, 2) ^ rotr(x, 13) ^ rotr(x, 22) }

// BigSigma1 is Σ1(x) = ROTR^6(x) ⊕ ROTR^11(x) ⊕ ROTR^25(x).
func BigSigma1(x uint32) uint32 { return rotr(x, 6) ^ rotr(x, 11) ^ rotr(x, 25) }

// Ch is the choose function: (x&y) ⊕ (^x&z).
func Ch(x, y, z uint32) uint32 { return (x & y) ^ (^x & z) }

// Maj is the majority function: (x&y) ⊕ (x&z) ⊕ (y&z).
func Maj(x, y, z uint32) uint32 { return (x & y) ^ (x & z) ^ (y & z) }

// LoadBlock reads the 16 big-endian words of a 64-byte block into w[0..15].
func LoadBlock(w *[64]uint32, block []byte) {
	for i := 0; i < 16; i++ {
		w[i] = binary.BigEndian.Uint32(block[4*i:])
	}
}

// Expand fills w[16..63] from the 16 big-endian words already in w[0..15]:
// W[t] = σ1(W[t-2]) + W[t-7] + σ0(W[t-15]) + W[t-16], all mod 2^32.
func Expand(w *[64]uint32) {
	for t := 16; t < 64; t++ {
		w[t] = SmallSigma1(w[t-2]) + w[t-7] + SmallSigma0(w[t-15]) + w[t-16]
	}
}
