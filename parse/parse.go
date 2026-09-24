// Package parse decodes the decimal text produced by fmtf back to the
// exact float64, using exact rational arithmetic for the final rounding.
package parse

import (
	"errors"
	"fmt"
	"math/big"
)

// Error sentinels, distinguishable with errors.Is.
var (
	ErrEmpty         = errors.New("parse: empty string")
	ErrInvalidChar   = errors.New("parse: invalid character")
	ErrExpOverflow   = errors.New("parse: decimal exponent out of range")
	ErrTooManyDigits = errors.New("parse: more than 17 significant digits")
)

// maxExp bounds the combined decimal exponent; fmtf never needs more than
// 5e-324 .. 1.8e+308, so |e10| <= 400 is generous.
const maxExp = 400

// maxDigits is the largest significant-digit count fmtf can emit.
const maxDigits = 17

// Float parses text of the form [-]d[.ddd][e±E] and returns the nearest
// float64 (ties to even), preserving the sign of zero.
func Float(s string) (float64, error) {
	if s == "" {
		return 0, ErrEmpty
	}
	i := 0
	neg := false
	if s[i] == '-' {
		neg = true
		i++
	}
	digits := make([]byte, 0, 20)
	frac := 0
	seenDot := false
	for i < len(s) && (isDigit(s[i]) || s[i] == '.') {
		if s[i] == '.' {
			if seenDot {
				return 0, invalid(s, i)
			}
			seenDot = true
		} else {
			digits = append(digits, s[i])
			if seenDot {
				frac++
			}
		}
		i++
	}
	if len(digits) == 0 {
		return 0, invalid(s, i)
	}
	exp := 0
	if i < len(s) && s[i] == 'e' {
		i++
		eNeg := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			eNeg = s[i] == '-'
			i++
		}
		if i >= len(s) || !isDigit(s[i]) {
			return 0, invalid(s, i)
		}
		for i < len(s) && isDigit(s[i]) {
			exp = exp*10 + int(s[i]-'0')
			if exp > maxExp {
				return 0, ErrExpOverflow
			}
			i++
		}
		if eNeg {
			exp = -exp
		}
	}
	if i < len(s) {
		return 0, invalid(s, i)
	}
	if n := sigDigits(digits); n > maxDigits {
		return 0, fmt.Errorf("%w: got %d", ErrTooManyDigits, n)
	}
	e10 := exp - frac
	if e10 > maxExp || e10 < -maxExp {
		return 0, ErrExpOverflow
	}
	return value(digits, e10, neg), nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func invalid(s string, i int) error {
	if i >= len(s) {
		return fmt.Errorf("%w at byte %d: unexpected end", ErrInvalidChar, i)
	}
	return fmt.Errorf("%w at byte %d: %q", ErrInvalidChar, i, s[i])
}

// sigDigits counts significant digits: leading zeros never count, and
// trailing zeros of fixed-notation integers are placeholders that fmtf
// only emits to position the decimal point, so they do not count either.
func sigDigits(d []byte) int {
	lo, hi := 0, len(d)
	for lo < hi && d[lo] == '0' {
		lo++
	}
	for hi > lo && d[hi-1] == '0' {
		hi--
	}
	return hi - lo
}

// value computes sign * D * 10^e10 exactly and rounds to float64.
func value(digits []byte, e10 int, neg bool) float64 {
	d, _ := new(big.Int).SetString(string(digits), 10)
	r := new(big.Rat).SetInt(d)
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(e10))), nil)
	if e10 >= 0 {
		r.Mul(r, new(big.Rat).SetInt(p))
	} else {
		r.Quo(r, new(big.Rat).SetInt(p))
	}
	f, _ := r.Float64()
	if neg {
		return -f
	}
	return f
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
