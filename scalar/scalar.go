// Package scalar classifies single Unicode scalar values. It depends on
// nothing and implements all range checks by hand.
package scalar

const (
	MaxScalar   = 0x10FFFF
	Replacement = 0xFFFD
	highLo      = 0xD800
	highHi      = 0xDBFF
	lowLo       = 0xDC00
	lowHi       = 0xDFFF
)

// Status is the result of decoding one unit from a byte stream.
type Status int

const (
	OK    Status = iota // a valid scalar was decoded
	Bad                 // an invalid unit of n bytes was consumed
	Short               // valid prefix so far, more bytes needed
)

// Valid reports whether v is a Unicode scalar value: in range and not a
// surrogate code point.
func Valid(v uint32) bool {
	return v <= MaxScalar && !(v >= highLo && v <= lowHi)
}

// IsHigh reports whether v is a high surrogate code unit.
func IsHigh(v uint32) bool { return v >= highLo && v <= highHi }

// IsLow reports whether v is a low surrogate code unit.
func IsLow(v uint32) bool { return v >= lowLo && v <= lowHi }

// Combine joins a high and a low surrogate into a scalar value.
func Combine(hi, lo uint32) uint32 {
	return 0x10000 + ((hi - highLo) << 10) + (lo - lowLo)
}
