package ontology

import "math/bits"

// scale is the fixed-point factor used internally for token accounting.
//
// Token amounts are stored as integer "nanotokens": amount * scale. Time is
// handled in nanoseconds, so a refill of `rate` tokens/second over
// `elapsed` nanoseconds is
//
//	rate * elapsed   nanotokens
//
// (because rate tokens/sec = rate/1e9 tokens/ns). Every intermediate value
// is an integer: the 0.7 token gained over 100ms at rate 7 is stored as
// 700_000_000 nanotokens and is never truncated. No float64 is involved.
const scale int64 = 1_000_000_000

// mul128 returns the signed 128-bit product of a and b as (hi, lo), where
// the signed result = hi<<64 + lo. This keeps refill math exact even when
// rate*elapsed overflows int64 (large durations or large rates).
func mul128(a, b int64) (hi, lo int64) {
	// bits.Mul64 works on unsigned values; convert using the identity
	// signed a = ua - sign*2^64.
	ua := uint64(a)
	ub := uint64(b)
	uhi, ulo := bits.Mul64(ua, ub)
	if a < 0 {
		uhi -= ub
	}
	if b < 0 {
		uhi -= ua
	}
	return int64(uhi), int64(ulo)
}

// div128 divides the signed 128-bit value hi<<64+lo by positive d and
// returns the integer quotient. ceil rounds toward positive infinity;
// otherwise it truncates toward zero. The intermediate quotient may be
// 128 bits; it panics only when d<=0 or the final result does not fit in
// int64.
func div128(hi, lo, d int64, ceil bool) int64 {
	if d <= 0 {
		panic("ontology: divisor must be positive")
	}
	neg := hi < 0
	uhi := uint64(hi)
	ulo := uint64(lo)
	if neg {
		// Two's-complement negation of the 128-bit value.
		ulo = ^ulo + 1
		uhi = ^uhi
		if ulo == 0 {
			uhi++
		}
	}
	// Produce the 128-bit unsigned quotient in two halves: divide the
	// high word first, feeding its remainder into the low word.
	y := uint64(d)
	qhi, rem := bits.Div64(0, uhi, y)
	qlo, rlo := bits.Div64(rem, ulo, y)
	if ceil && rlo != 0 {
		qlo++
		if qlo == 0 {
			qhi++
		}
	}
	switch {
	case qhi != 0:
		panic("ontology: fixed-point quotient overflow")
	case qlo > 1<<63:
		panic("ontology: fixed-point quotient overflow")
	case qlo == 1<<63 && !neg:
		panic("ontology: fixed-point quotient overflow")
	}
	result := int64(qlo)
	if neg {
		result = -result
	}
	return result
}

// refillNanotokens returns the exact integer number of nanotokens gained
// over elapsed nanoseconds at rate tokens/second.
//
// In tokens the gain is rate*elapsed/1e9; in nanotokens (tokens*1e9) that
// is simply rate*elapsed. The result is integer nanotokens, so sub-token
// fractions persist in the bucket until they add up.
func refillNanotokens(rate, elapsed int64) int64 {
	if elapsed <= 0 || rate <= 0 {
		return 0
	}
	hi, lo := mul128(rate, elapsed) // nanotokens, exact integer
	if hi != 0 || uint64(lo) >= 1<<63 {
		panic("ontology: refill overflow")
	}
	return lo
}

// waitNanos returns the minimum positive number of nanoseconds needed to
// accumulate `need` nanotokens at `rate` tokens/second, rounded up.
//
// One token per second means one nanotoken per nanosecond, so at rate r
// the wait in nanoseconds is need/r, rounded up: ceil(need/rate).
func waitNanos(need, rate int64) int64 {
	if need <= 0 || rate <= 0 {
		return 0
	}
	hi, lo := mul128(need, 1)
	return div128(hi, lo, rate, true)
}
