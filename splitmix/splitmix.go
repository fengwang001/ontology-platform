// Package splitmix implements the SplitMix64 mixing function and seed
// derivation for the counter-mode stream cipher. It depends on no other
// package in this module.
package splitmix

const (
	gamma      uint64 = 0x9E3779B97F4A7C15
	mixerA     uint64 = 0xBF58476D1CE4E5B9
	mixerB     uint64 = 0x94D049BB133111EB
	shiftSmall        = 30
	shiftMid          = 27
	shiftFinal        = 31
)

// SplitMix64 applies the SplitMix64 finalizer to x with uint64 wrap-around:
//
//	x += 0x9E3779B97F4A7C15
//	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
//	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
//	return x ^ (x >> 31)
func SplitMix64(x uint64) uint64 {
	x += gamma
	x = (x ^ (x >> shiftSmall)) * mixerA
	x = (x ^ (x >> shiftMid)) * mixerB
	return x ^ (x >> shiftFinal)
}

// Seed derives the keystream seed: splitmix64(key ^ nonce).
func Seed(key, nonce uint64) uint64 {
	return SplitMix64(key ^ nonce)
}
