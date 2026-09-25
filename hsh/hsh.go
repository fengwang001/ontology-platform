// Package hsh provides the deterministic 64-bit hash (FNV-1a + splitmix64
// finalizer) and the bucket-index / rank extraction used by the HLL sketch.
package hsh

import "math/bits"

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Hash maps a string to a deterministic 64-bit value: FNV-1a over the UTF-8
// bytes, then a splitmix64 finalizer. All arithmetic is mod 2^64.
func Hash(s string) uint64 {
	h := uint64(fnvOffset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime
	}
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	h ^= h >> 31
	return h
}

// Bucket extracts the register index j = h & (m-1) (low p bits) and the rank
// rho = 1 + leading zeros of w = h >> p inside the (64-p)-bit field.
// w == 0 yields rho = 64-p+1.
func Bucket(h uint64, p uint) (j uint32, rho uint8) {
	j = uint32(h & ((1 << p) - 1))
	w := h >> p
	if w == 0 {
		return j, uint8(64 - p + 1)
	}
	return j, uint8(bits.LeadingZeros64(w) - int(p) + 1)
}
