// Package dec generates the shortest decimal significant digits that
// round-trip a positive float64, using exact rational arithmetic only.
package dec

import (
	"math"
	"math/big"
	"sync/atomic"
)

var checks atomic.Int64 // diagnostic: exact round-trip checks performed

// Checks returns how many exact round-trip checks have been executed.
func Checks() int64 { return checks.Load() }

// ResetChecks resets the diagnostic counter.
func ResetChecks() { checks.Store(0) }

// Shortest returns the shortest significant digit string (no trailing
// zeros) and the scientific decimal exponent exp10 for the positive value
// m * 2^e2, so that value = 0.d1d2... * 10^(exp10+1) equivalently
// value = D * 10^(exp10-len(digits)+1) with D = int(digits).
// The first digit count (from 1 up) whose nearest decimal rounds back to
// the original float wins; 17 always suffices.
func Shortest(m uint64, e2 int) (digits string, exp10 int) {
	r := ratOf(m, e2)
	base := floorLog10(r)
	want := math.Float64bits(math.Ldexp(float64(m), e2))
	for k := 1; k <= 17; k++ {
		e10 := base
		q := roundSig(r, e10, k)
		if top := pow10(k); q.Cmp(top) >= 0 { // carried to k+1 digits
			q.Quo(q, big.NewInt(10))
			e10++
		}
		s := trimZeros(q.String())
		if roundTrips(s, e10, want) {
			return s, e10
		}
	}
	panic("dec: 17 significant digits must round-trip")
}

// ratOf builds the exact rational m * 2^e2.
func ratOf(m uint64, e2 int) *big.Rat {
	r := new(big.Rat).SetUint64(m)
	if e2 >= 0 {
		return r.Mul(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(e2))))
	}
	return r.Quo(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(-e2))))
}

// pow10 returns 10^e as a big.Int; e must be >= 0.
func pow10(e int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(e)), nil)
}

// pow10Rat returns 10^e as an exact big.Rat for any e.
func pow10Rat(e int) *big.Rat {
	if e >= 0 {
		return new(big.Rat).SetInt(pow10(e))
	}
	return new(big.Rat).SetFrac(big.NewInt(1), pow10(-e))
}

// floorLog10 returns the exact e with 10^e <= r < 10^(e+1), using a float
// estimate corrected by exact comparison.
func floorLog10(r *big.Rat) int {
	f, _ := r.Float64()
	e := int(math.Floor(math.Log10(f)))
	for r.Cmp(pow10Rat(e+1)) >= 0 {
		e++
	}
	for r.Cmp(pow10Rat(e)) < 0 {
		e--
	}
	return e
}

// roundSig rounds r (which lies in [10^e10, 10^(e10+1))) to k significant
// decimal digits, half to even, returning the k-digit integer.
func roundSig(r *big.Rat, e10, k int) *big.Int {
	scale := k - 1 - e10
	scaled := new(big.Rat)
	if scale >= 0 {
		scaled.Mul(r, pow10Rat(scale))
	} else {
		scaled.Quo(r, pow10Rat(-scale))
	}
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(scaled.Num(), scaled.Denom(), rem)
	switch c := new(big.Int).Lsh(rem, 1).Cmp(scaled.Denom()); {
	case c > 0, c == 0 && q.Bit(0) == 1:
		q.Add(q, big.NewInt(1))
	}
	return q
}

// trimZeros strips trailing zeros off a digit string.
func trimZeros(s string) string {
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	return s
}

// roundTrips checks with exact arithmetic whether the decimal
// digits * 10^(exp10-len(digits)+1) parses back to the float with the
// given bit pattern. big.Rat.Float64 rounds to nearest, ties to even.
func roundTrips(digits string, exp10 int, want uint64) bool {
	checks.Add(1)
	d, _ := new(big.Int).SetString(digits, 10)
	e := exp10 - len(digits) + 1
	r := new(big.Rat).SetInt(d)
	if e >= 0 {
		r.Mul(r, pow10Rat(e))
	} else {
		r.Quo(r, pow10Rat(-e))
	}
	f, _ := r.Float64()
	return math.Float64bits(f) == want
}
