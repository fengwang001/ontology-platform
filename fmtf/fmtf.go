// Package fmtf assembles the shortest round-tripping decimal text for a
// float64. The choice between fixed and scientific notation is a pure
// function of the value's scientific exponent E: fixed iff -4 <= E < 21.
package fmtf

import (
	"errors"
	"strconv"
	"strings"

	"ontology/bits"
	"ontology/dec"
)

// ErrNaN and ErrInf are returned because no decimal text form is defined
// for NaN or infinities; silently emitting "NaN"/"Inf" strings would break
// the encode-then-decode round-trip contract.
var (
	ErrNaN = errors.New("fmtf: NaN has no decimal text form")
	ErrInf = errors.New("fmtf: infinity has no decimal text form")
)

// Encode returns the shortest decimal text that parses back to f exactly.
func Encode(f float64) (string, error) {
	p := bits.Split(f)
	switch {
	case p.IsNaN():
		return "", ErrNaN
	case p.IsInf():
		return "", ErrInf
	case p.IsZero():
		if p.Sign {
			return "-0", nil
		}
		return "0", nil
	}
	digits, exp10 := dec.Shortest(p.IntValue())
	s := format(digits, exp10)
	if p.Sign {
		return "-" + s, nil
	}
	return s, nil
}

// format renders digits (no trailing zeros) with scientific exponent e,
// i.e. value = d.ddd... * 10^e. Fixed notation iff -4 <= e < 21.
func format(digits string, e int) string {
	if e < -4 || e >= 21 {
		return scientific(digits, e)
	}
	n := len(digits)
	switch {
	case e >= n-1: // integer with trailing zeros
		return digits + strings.Repeat("0", e-n+1)
	case e >= 0: // point inside the digits
		return digits[:e+1] + "." + digits[e+1:]
	default: // 0.00ddd
		return "0." + strings.Repeat("0", -e-1) + digits
	}
}

// scientific renders d[.ddd]e±E with an always-signed exponent.
func scientific(digits string, e int) string {
	var b strings.Builder
	b.WriteByte(digits[0])
	if len(digits) > 1 {
		b.WriteByte('.')
		b.WriteString(digits[1:])
	}
	b.WriteByte('e')
	if e < 0 {
		b.WriteByte('-')
		e = -e
	} else {
		b.WriteByte('+')
	}
	b.WriteString(strconv.Itoa(e))
	return b.String()
}
