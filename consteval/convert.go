package consteval

import (
	"math"
	"math/big"
)

// convertTo converts a constant (untyped or typed) to the target type,
// enforcing representability:
//
//   - to an integer type: the value must be exactly integral, otherwise
//     ErrTruncation; and inside the type's range, otherwise ErrOverflow.
//   - to float64: the value rounds to the nearest double (ties to even);
//     if the rounded result is infinite, ErrOverflow.
//   - to bool/string: only the matching category converts; anything else is
//     ErrTypeMismatch.
func convertTo(c *Const, t Type) (*Const, *Error) {
	if c.typ == t {
		return c, nil
	}
	switch {
	case t.IsInt():
		return convertToIntType(c, t)
	case t == TypeFloat64:
		return convertToFloat(c)
	case t == TypeBool:
		if c.kind == KindBool {
			return typedBool(c.b), nil
		}
	case t == TypeString:
		if c.kind == KindString {
			return typedString(c.s), nil
		}
	}
	return nil, errf(ErrTypeMismatch, "cannot convert %s to %s", c, t)
}

// convertToIntType converts c to the integer type t.
func convertToIntType(c *Const, t Type) (*Const, *Error) {
	var v *big.Int
	switch c.category() {
	case catInt:
		v = c.intValue()
	case catRat:
		r := c.r
		if !r.IsInt() {
			return nil, errf(ErrTruncation, "conversion of %s to %s discards fraction", c, t)
		}
		v = new(big.Int).Set(r.Num())
	case catFloat:
		f := c.f
		if math.IsInf(f, 0) || math.IsNaN(f) || f != math.Trunc(f) {
			return nil, errf(ErrTruncation, "conversion of %s to %s discards fraction", c, t)
		}
		if !floatFitsIntType(f, t) {
			return nil, errf(ErrOverflow, "conversion of %s to %s is out of range", c, t)
		}
		return makeTypedInt(t, f), nil
	default:
		return nil, errf(ErrTypeMismatch, "cannot convert %s to %s", c, t)
	}
	min, max := intRange(t)
	if v.Cmp(min) < 0 || v.Cmp(max) > 0 {
		return nil, errf(ErrOverflow, "value %s is out of range for %s", v, t)
	}
	return makeTypedIntBig(t, v), nil
}

// convertToFloat converts c to float64 with round-to-nearest-even.
func convertToFloat(c *Const) (*Const, *Error) {
	switch c.category() {
	case catInt, catRat:
		var r *big.Rat
		if c.typ == TypeNone && c.kind == KindInt {
			r = new(big.Rat).SetInt(c.i)
		} else if c.typ == TypeNone {
			r = c.r
		} else {
			r = new(big.Rat).SetInt(c.intValue())
		}
		f, overflow := ratToFloat64(r)
		if overflow {
			return nil, errf(ErrOverflow, "conversion of %s to float64 rounds to infinity", c)
		}
		return typedFloat(f), nil
	case catFloat:
		return typedFloat(c.f), nil
	}
	return nil, errf(ErrTypeMismatch, "cannot convert %s to float64", c)
}

// makeTypedInt builds a typed integer constant from an integral float
// already known to fit.
func makeTypedInt(t Type, f float64) *Const {
	if t.Signed() {
		return typedSigned(t, int64(f))
	}
	return typedUnsigned(t, uint64(f))
}

// makeTypedIntBig builds a typed integer constant from a big.Int already
// known to fit.
func makeTypedIntBig(t Type, v *big.Int) *Const {
	if t.Signed() {
		return typedSigned(t, v.Int64())
	}
	return typedUnsigned(t, v.Uint64())
}

// floatFitsIntType reports whether the integral finite float f lies
// inside the range of integer type t. All bounds used are exactly
// representable as float64, so the comparisons are exact.
func floatFitsIntType(f float64, t Type) bool {
	w := t.Bits()
	if t.Signed() {
		lo := -math.Exp2(float64(w - 1))
		hi := math.Exp2(float64(w - 1)) // exclusive upper bound
		return f >= lo && f < hi
	}
	return f >= 0 && f < math.Exp2(float64(w))
}

// ratToFloat64 rounds an exact rational to the nearest float64, ties to
// even. overflow is true when the rounded result would be infinite
// (including the tie at the top of the range, which rounds to the even
// "mantissa" of 2^1024, i.e. infinity).
func ratToFloat64(r *big.Rat) (f float64, overflow bool) {
	if r.Sign() == 0 {
		return 0, false
	}
	neg := r.Sign() < 0
	num := new(big.Int).Abs(r.Num())
	den := r.Denom() // always positive

	// e = floor(log2(num/den)).
	e := num.BitLen() - den.BitLen()
	if e >= 0 {
		if num.Cmp(new(big.Int).Lsh(den, uint(e))) < 0 {
			e--
		}
	} else {
		if new(big.Int).Lsh(num, uint(-e)).Cmp(den) < 0 {
			e--
		}
	}

	// Round to a multiple of 2^s, keeping 53 significant bits; in the
	// subnormal range the quantum is fixed at 2^-1074.
	s := e - 52
	if s < -1074 {
		s = -1074
	}

	// t = (num/den) / 2^s = dividend/divisor; round t to nearest integer,
	// ties to even.
	var dividend, divisor *big.Int
	if s >= 0 {
		dividend = num
		divisor = new(big.Int).Lsh(den, uint(s))
	} else {
		dividend = new(big.Int).Lsh(num, uint(-s))
		divisor = den
	}
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(dividend, divisor, rem)
	switch new(big.Int).Lsh(rem, 1).Cmp(divisor) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}

	if q.Sign() == 0 {
		if neg {
			return math.Copysign(0, -1), false
		}
		return 0, false
	}
	// Rounding may have pushed q to 2^53; renormalize.
	if q.BitLen() > 53 {
		q.Rsh(q, 1)
		s++
	}
	// The value is q * 2^s with q <= 2^53; its binary exponent must not
	// exceed 1023.
	if s+q.BitLen()-1 > 1023 {
		return 0, true
	}
	f = math.Ldexp(float64(q.Uint64()), s)
	if math.IsInf(f, 0) {
		return 0, true
	}
	if neg {
		f = -f
	}
	return f, false
}
