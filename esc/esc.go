// Package esc defines the byte-escaping rules for flag-byte framing.
package esc

const (
	Flag = 0x7E // frame delimiter
	Esc  = 0x7D // escape byte
	xor  = 0x20 // escape mapping: b -> b^0x20
)

// NeedsEscape reports whether b must be escaped inside a frame payload.
func NeedsEscape(b byte) bool { return b == Flag || b == Esc }

// Map returns the wire byte following Esc for payload byte b, and likewise
// restores an escaped byte to its original (XOR by 0x20 is an involution).
func Map(b byte) byte { return b ^ xor }
