// Package modarith implements modular arithmetic over a prime field:
// modular multiplication, modular addition, and square-and-multiply
// modular exponentiation. It depends on no other package in the module.
package modarith

import (
	"math/bits"
	"sync/atomic"
)

// lastMulCount records the number of modular multiplications performed by
// the most recent ModPow call. It is deliberately unexported and is not
// reachable through any exported function or method.
var lastMulCount atomic.Uint64

// Mul returns a*b mod p. Callers must keep p small enough that the
// product of two residues cannot overflow uint64.
func Mul(a, b, p uint64) uint64 {
	return (a % p) * (b % p) % p
}

// Add returns a+b mod p.
func Add(a, b, p uint64) uint64 {
	return (a%p + b%p) % p
}

// ModPow returns base^e mod p using square-and-multiply, scanning the
// exponent's bits from the most significant to the least significant:
// for each bit, square r; if the bit is 1, also multiply by base.
func ModPow(base, e, p uint64) uint64 {
	n := bits.Len64(e)
	bs := make([]byte, n)
	for i := 0; i < n; i++ {
		bs[n-1-i] = byte(e >> i & 1)
	}
	return modpowBits(base, p, bs)
}

// modpowBits is the square-and-multiply core over an explicit MSB-first
// bit slice, so tests can drive exponents far wider than uint64.
func modpowBits(base, p uint64, bs []byte) uint64 {
	b := base % p
	r := uint64(1)
	var cnt uint64
	for _, bit := range bs {
		r = Mul(r, r, p)
		cnt++
		if bit == 1 {
			r = Mul(r, b, p)
			cnt++
		}
	}
	lastMulCount.Store(cnt)
	return r
}
