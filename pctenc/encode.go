package pctenc

import "strings"

const upperHex = "0123456789ABCDEF"

// Encode percent-encodes s according to mode m.
//
// Unreserved characters (A-Z a-z 0-9 - _ . ~) are never encoded.
// Bytes outside the mode's safe set are encoded one byte at a time
// with uppercase hex digits, so non-ASCII text is emitted as its
// UTF-8 byte sequence. If nothing needs encoding, s is returned
// unchanged.
func Encode(s string, m Mode) string {
	safe := safeFor(m)
	need := 0
	for i := 0; i < len(s); i++ {
		if !safe.has(s[i]) {
			need++
		}
	}
	if need == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 2*need)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if safe.has(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0x0f])
		}
	}
	return b.String()
}
