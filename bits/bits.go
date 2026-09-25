// Package bits provides a fixed-length bit array indexed from 0.
package bits

// Array is a fixed-length bit array. It is safe for concurrent reads;
// concurrent Set and Get on the same word require external synchronization.
type Array struct {
	words []uint64
	n     uint64
}

// New returns an Array of n bits, all zero.
func New(n uint64) *Array {
	return &Array{words: make([]uint64, (n+63)/64), n: n}
}

// Set marks bit i as 1. An out-of-range i is ignored.
func (a *Array) Set(i uint64) {
	if i < a.n {
		a.words[i/64] |= 1 << (i % 64)
	}
}

// Get reports whether bit i is 1. An out-of-range i reads as 0.
func (a *Array) Get(i uint64) bool {
	return i < a.n && a.words[i/64]&(1<<(i%64)) != 0
}

// Len returns the number of bits in the array.
func (a *Array) Len() uint64 { return a.n }
