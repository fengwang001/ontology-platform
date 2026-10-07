package consteval

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// ratOf builds a rational from numerator and denominator decimal
// strings.
func ratOf(num, den string) *big.Rat {
	n, _ := new(big.Int).SetString(num, 10)
	d, _ := new(big.Int).SetString(den, 10)
	return new(big.Rat).SetFrac(n, d)
}

// wantFloat converts the rational to float64 via an OpConvert node and
// checks the exact result.
func wantFloat(t *testing.T, r *big.Rat, want float64) {
	t.Helper()
	expr := Convert(&Node{Op: OpLit, Lit: Lit{Kind: KindRational, Rat: r}}, "float64")
	v := mustEval(t, expr)
	if v.Type() != TypeFloat64 {
		t.Fatalf("got %s, want float64", v)
	}
	got := v.Float64()
	if got != want && !(got == 0 && want == 0) {
		t.Fatalf("ratToFloat64(%s) = %v (%#x), want %v (%#x)",
			r.RatString(), got, math.Float64bits(got), want, math.Float64bits(want))
	}
}

// wantFloatErr requires the conversion of r to float64 to overflow.
func wantFloatErr(t *testing.T, r *big.Rat) {
	t.Helper()
	expr := Convert(&Node{Op: OpLit, Lit: Lit{Kind: KindRational, Rat: r}}, "float64")
	mustErr(t, expr, ErrOverflow)
}

// Rounding to the nearest double, ties to even.
func TestFloatRoundTiesToEven(t *testing.T) {
	p53 := pow2(53) // 2^53

	// 1 + 2^-53 is the exact midpoint between 1.0 (even mantissa) and
	// 1 + 2^-52: ties to even rounds down to 1.0.
	r := ratOf(new(big.Int).Add(p53, big.NewInt(1)).String(), p53.String())
	wantFloat(t, r, 1.0)

	// 1 + 3*2^-53 is the exact midpoint between 1+2^-52 (odd mantissa)
	// and 1+2^-51 (even mantissa): rounds up to 1 + 2^-51.
	r = ratOf(new(big.Int).Add(p53, big.NewInt(3)).String(), p53.String())
	wantFloat(t, r, 1+math.Ldexp(1, -51))

	// 1 + 2^-53 + 2^-70 is just above the midpoint: rounds up.
	num := new(big.Int).Add(p53, big.NewInt(1))
	num.Lsh(num, 17).Add(num, big.NewInt(1)) // scale by 2^17 then add 1
	r = ratOf(num.String(), new(big.Int).Lsh(p53, 17).String())
	wantFloat(t, r, 1+math.Ldexp(1, -52))

	// Integer kind converts the same way: 2^53 + 1 is the midpoint
	// between 2^53 and 2^53+2; ties to even rounds to 2^53.
	expr := Convert(BigIntLit(new(big.Int).Add(p53, big.NewInt(1))), "float64")
	v := mustEval(t, expr)
	if v.Float64() != math.Exp2(53) {
		t.Fatalf("2^53+1 -> %v, want 2^53", v.Float64())
	}
	// 2^53 + 3 is the midpoint between 2^53+2 and 2^53+4; the even
	// mantissa is 2^53+4.
	expr = Convert(BigIntLit(new(big.Int).Add(p53, big.NewInt(3))), "float64")
	v = mustEval(t, expr)
	if v.Float64() != math.Exp2(53)+4 {
		t.Fatalf("2^53+3 -> %v, want 2^53+4", v.Float64())
	}

	// Negative values round symmetrically.
	r = ratOf("-"+new(big.Int).Add(p53, big.NewInt(1)).String(), p53.String())
	wantFloat(t, r, -1.0)
}

// Rounding that lands on infinity is an overflow error.
func TestFloatRoundToInfinity(t *testing.T) {
	// The largest finite double is (2^53-1)*2^971.
	maxFin := new(big.Int).Sub(pow2(53), big.NewInt(1))
	maxFin.Lsh(maxFin, 971)
	wantFloat(t, new(big.Rat).SetInt(maxFin), math.MaxFloat64)

	// The exact midpoint between MaxFloat64 and 2^1024 ties to the even
	// "mantissa" of 2^1024, i.e. infinity: overflow.
	mid := new(big.Int).Sub(pow2(1024), pow2(970))
	wantFloatErr(t, new(big.Rat).SetInt(mid))

	// Anything larger overflows as well.
	wantFloatErr(t, new(big.Rat).SetInt(pow2(1024)))

	// Just below the midpoint still rounds to MaxFloat64.
	below := new(big.Int).Sub(mid, pow2(969))
	wantFloat(t, new(big.Rat).SetInt(below), math.MaxFloat64)
}

// Subnormal rounding uses a fixed quantum of 2^-1074.
func TestFloatSubnormalRounding(t *testing.T) {
	// 2^-1074 is exactly the smallest subnormal.
	wantFloat(t, new(big.Rat).SetFrac(big.NewInt(1), pow2(1074)), math.Ldexp(1, -1074))
	// 2^-1075 is the exact midpoint between 0 (even) and 2^-1074: 0.
	wantFloat(t, new(big.Rat).SetFrac(big.NewInt(1), pow2(1075)), 0)
	// 3*2^-1075 is the exact midpoint between 2^-1074 (odd) and
	// 2^-1073 (even): rounds to 2^-1073.
	wantFloat(t, new(big.Rat).SetFrac(big.NewInt(3), pow2(1075)), math.Ldexp(1, -1073))
	// Tiny values round to zero.
	wantFloat(t, new(big.Rat).SetFrac(big.NewInt(1), pow2(2000)), 0)
}

// Conversion to integer types distinguishes truncation (value not
// integral) from overflow (integral but out of range).
func TestIntConversionBoundaries(t *testing.T) {
	// Rational kind with integral value converts fine.
	wantTypedInt(t, mustEval(t, Convert(RatLit("3/1"), "int64")), TypeInt64, 3)
	wantTypedUint(t, mustEval(t, Convert(RatLit("6/3"), "uint8")), TypeUint8, 2)

	// Non-integral rational: truncation, not overflow.
	mustErr(t, Convert(RatLit("7/2"), "int64"), ErrTruncation)
	mustErr(t, Convert(RatLit("1/3"), "uint8"), ErrTruncation)

	// Integer kind at and beyond type bounds.
	wantTypedInt(t, mustEval(t, Convert(IntLit("9223372036854775807"), "int64")), TypeInt64, math.MaxInt64)
	mustErr(t, Convert(IntLit("9223372036854775808"), "int64"), ErrOverflow)
	wantTypedInt(t, mustEval(t, Convert(IntLit("-9223372036854775808"), "int64")), TypeInt64, math.MinInt64)
	mustErr(t, Convert(IntLit("-9223372036854775809"), "int64"), ErrOverflow)

	wantTypedUint(t, mustEval(t, Convert(IntLit("255"), "uint8")), TypeUint8, 255)
	mustErr(t, Convert(IntLit("256"), "uint8"), ErrOverflow)
	mustErr(t, Convert(IntLit("-1"), "uint8"), ErrOverflow)
	wantTypedUint(t, mustEval(t, Convert(IntLit("18446744073709551615"), "uint64")), TypeUint64, math.MaxUint64)
	mustErr(t, Convert(IntLit("18446744073709551616"), "uint64"), ErrOverflow)

	// Typed float to integer: integral check first, then range.
	wantTypedInt(t, mustEval(t, Convert(Convert(RatLit("3/1"), "float64"), "int64")), TypeInt64, 3)
	mustErr(t, Convert(Convert(RatLit("5/2"), "float64"), "int64"), ErrTruncation)
	mustErr(t, Convert(Convert(RatLit("1e30"), "float64"), "int8"), ErrOverflow)

	// Cross-category conversions are type mismatches.
	mustErr(t, Convert(BoolLit(true), "int8"), ErrTypeMismatch)
	mustErr(t, Convert(StrLit("1"), "float64"), ErrTypeMismatch)
	mustErr(t, Convert(IntLit("1"), "bool"), ErrTypeMismatch)
	mustErr(t, Convert(IntLit("1"), "string"), ErrTypeMismatch)

	// Identity conversions.
	wantTypedInt(t, mustEval(t, Convert(Convert(IntLit("7"), "int8"), "int8")), TypeInt8, 7)
	v := mustEval(t, Convert(Convert(IntLit("7"), "int8"), "int16"))
	wantTypedInt(t, v, TypeInt16, 7)
}

// Cross-check ratToFloat64 against big.Float with 53-bit precision and
// ToNearestEven on random rationals in the normal-exponent range.
func TestRatToFloat64RandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261008))
	ref := new(big.Float).SetPrec(53).SetMode(big.ToNearestEven)
	for i := 0; i < 5000; i++ {
		num := new(big.Int).Rand(rng, pow2(200))
		if rng.Intn(2) == 0 {
			num.Neg(num)
		}
		den := new(big.Int).Rand(rng, pow2(200))
		if den.Sign() == 0 {
			den.SetInt64(1)
		}
		r := new(big.Rat).SetFrac(num, den)
		got, overflow := ratToFloat64(r)
		want, _ := ref.SetRat(r).Float64()
		// Restrict to the normal range where the big.Float reference is
		// exact (no subnormal double-rounding, no overflow).
		if math.IsInf(want, 0) || want == 0 || !isNormal(want) {
			continue
		}
		if overflow {
			t.Fatalf("ratToFloat64(%s) overflowed, reference %v", r.RatString(), want)
		}
		if got != want {
			t.Fatalf("ratToFloat64(%s) = %v (%#x), reference %v (%#x)",
				r.RatString(), got, math.Float64bits(got), want, math.Float64bits(want))
		}
	}
}

func isNormal(f float64) bool {
	return math.Float64bits(f)>>52&0x7ff != 0
}
