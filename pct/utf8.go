package pct

import "unicode/utf8"

// validator incrementally validates one UTF-8 byte stream in a single pass,
// remembering the start offset of the rune currently being assembled.
type validator struct {
	buf      [utf8.UTFMax]byte
	n        int
	want     int
	seqStart int
}

func newValidator() *validator { return &validator{} }

func leadLen(b byte) (int, bool) {
	switch {
	case b < 0x80:
		return 1, true
	case b < 0xC2:
		return 0, false // continuation byte or overlong C0/C1 lead
	case b < 0xE0:
		return 2, true
	case b < 0xF0:
		return 3, true
	case b < 0xF5:
		return 4, true
	default:
		return 0, false // F5-FF cannot lead a valid rune
	}
}

// feed appends one logical byte, returning the invalid-run start offset when
// the byte stream is not valid UTF-8 (ok=false).
func (v *validator) feed(b byte, off int) (int, bool) {
	if v.n == 0 {
		if b < 0x80 {
			return 0, true
		}
		want, ok := leadLen(b)
		if !ok {
			return off, false
		}
		v.buf[0] = b
		v.n = 1
		v.want = want
		v.seqStart = off
		return 0, true
	}
	if b < 0x80 || b >= 0xC0 {
		return v.seqStart, false // expected a continuation byte
	}
	v.buf[v.n] = b
	v.n++
	if v.n < v.want {
		return 0, true
	}
	r, _ := utf8.DecodeRune(v.buf[:v.want])
	if r == utf8.RuneError {
		return v.seqStart, false
	}
	v.n = 0
	return 0, true
}

// end reports whether the stream ended on a rune boundary.
func (v *validator) end() (int, bool) {
	if v.n != 0 {
		return v.seqStart, false
	}
	return 0, true
}
