// Package splitmix implements the SplitMix64 mixing function and seed
// derivation used by the counter-mode stream cipher. It depends on no
// other package in this module.
package splitmix

const (
	gamma uint64 = 0x9E3779B97F4A7C15
	mul1  uint64 = 0xBF58476D1CE4E5B9
	mul2  uint64 = 0x94D049BB133111EB
)

// SplitMix64 is the fixed stateless mixing function from the spec:
// add gamma, two multiply-xorshift rounds, final xor-shift. All arithmetic
// is uint64 and wraps naturally.
func SplitMix64(x uint64) uint64 {
	x += gamma
	x = (x ^ (x >> 30)) * mul1
	x = (x ^ (x >> 27)) * mul2
	return x ^ (x >> 31)
}

// Seed derives the keystream seed: splitmix64(key XOR nonce).
func Seed(key, nonce uint64) uint64 {
	return SplitMix64(key ^ nonce)
}
