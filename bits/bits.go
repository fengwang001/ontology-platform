// Package bits splits a float64 into its IEEE 754 binary components.
package bits

import "math"

// Parts holds the raw IEEE 754 fields of a float64.
type Parts struct {
	Sign bool   // sign bit
	Exp  int    // raw biased exponent (0..2047)
	Mant uint64 // raw 52-bit mantissa
}

// Split extracts sign, exponent and mantissa using math.Float64bits.
func Split(f float64) Parts {
	b := math.Float64bits(f)
	return Parts{
		Sign: b>>63 != 0,
		Exp:  int(b>>52) & 0x7ff,
		Mant: b & (1<<52 - 1),
	}
}

// IsNaN reports whether f is NaN.
func (p Parts) IsNaN() bool { return p.Exp == 0x7ff && p.Mant != 0 }

// IsInf reports whether f is +Inf or -Inf.
func (p Parts) IsInf() bool { return p.Exp == 0x7ff && p.Mant == 0 }

// IsZero reports whether f is +0 or -0.
func (p Parts) IsZero() bool { return p.Exp == 0 && p.Mant == 0 }

// IntValue returns exact integers m and e2 such that the absolute value of
// the float equals m * 2^e2. For normals m = 2^52|mant, e2 = Exp-1075;
// for subnormals m = mant, e2 = -1074. Zero yields m = 0.
func (p Parts) IntValue() (m uint64, e2 int) {
	if p.Exp == 0 {
		return p.Mant, -1074
	}
	return p.Mant | 1<<52, p.Exp - 1075
}
