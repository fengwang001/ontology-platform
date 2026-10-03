// Package scalar classifies single Unicode scalar values.
package scalar

const (
	Max         = 0x10FFFF
	HiMin       = 0xD800
	HiMax       = 0xDBFF
	LoMin       = 0xDC00
	LoMax       = 0xDFFF
	Replacement = 0xFFFD
)

// Status is the result of decoding one unit from a byte stream.
type Status int

const (
	OK    Status = iota // a valid scalar
	Bad                 // an invalid unit (maximal subpart)
	Short               // a valid prefix, truncated by end of input
)

// Valid reports whether r is a Unicode scalar value.
func Valid(r int32) bool {
	return 0 <= r && r <= Max && !Surrogate(r)
}

// Surrogate reports whether r lies in the surrogate range.
func Surrogate(r int32) bool {
	return HiMin <= r && r <= LoMax
}

// IsHi reports whether w is a high surrogate code unit.
func IsHi(w uint16) bool {
	return HiMin <= int32(w) && int32(w) <= HiMax
}

// IsLo reports whether w is a low surrogate code unit.
func IsLo(w uint16) bool {
	return LoMin <= int32(w) && int32(w) <= LoMax
}

// Combine merges a surrogate pair into a scalar value.
func Combine(hi, lo uint16) int32 {
	return 0x10000 + ((int32(hi) - HiMin) << 10) + (int32(lo) - LoMin)
}
