// Package bits splits a finite float64 into sign, exponent and mantissa
// fields using math.Float64bits, without any floating-point arithmetic.
package bits

import (
	"math"
	"math/big"
)

const (
	// Bias is the IEEE 754 binary64 exponent bias.
	Bias = 1023
	// FracBits is the number of stored fraction bits.
	FracBits = 52
	// MinExp is the unbiased exponent of the smallest subnormal.
	MinExp = -1074
)

// Parts is the exact decomposition x = (-1)^Sign * Mantissa * 2^Exp.
// For zero, Mantissa is 0 and Zero is true (Sign still records +/-).
type Parts struct {
	Sign     uint
	Mantissa *big.Int
	Exp      int
	Zero     bool
}

// Split returns the sign / exponent / mantissa of f.
// NaN and Inf must be rejected by the caller; Split only takes finite f.
func Split(f float64) Parts {
	b := math.Float64bits(f)
	p := Parts{Sign: b >> 63, Mantissa: new(big.Int)}
	frac := b & ((uint64(1) << FracBits) - 1)
	biased := int((b >> FracBits) & 0x7ff)
	switch {
	case biased == 0 && frac == 0:
		p.Zero = true
	case biased == 0: // subnormal: implicit bit is 0, fixed minimal exponent
		p.Mantissa.SetUint64(frac)
		p.Exp = MinExp
	default: // normal: implicit leading 1
		p.Mantissa.SetUint64((uint64(1) << FracBits) | frac)
		p.Exp = biased - Bias - FracBits
	}
	return p
}

// Rat returns the exact rational value of p as numerator / denominator,
// both powers of two. For zero it returns (0, 1).
func Rat(p Parts) (*big.Int, *big.Int) {
	if p.Zero {
		return new(big.Int), big.NewInt(1)
	}
	if p.Exp >= 0 {
		num := new(big.Int).Lsh(p.Mantissa, uint(p.Exp))
		return num, big.NewInt(1)
	}
	den := new(big.Int).Lsh(big.NewInt(1), uint(-p.Exp))
	return new(big.Int).Set(p.Mantissa), den
}

// IsFinite reports whether f is neither NaN nor an infinity.
func IsFinite(f float64) bool {
	b := math.Float64bits(f)
	return (b>>FracBits)&0x7ff != 0x7ff
}
