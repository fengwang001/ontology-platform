package pct

// unreserved per RFC 3989: ALPHA / DIGIT / "-" / "." / "_" / "~".
// Only a percent-encoded unreserved character may be turned back into a
// literal, because doing so never changes how a URL is parsed.
func isUnreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '-' || c == '.' || c == '_' || c == '~':
		return true
	}
	return false
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// nibbleIsUpper reports whether raw matches value using an uppercase hex digit.
func nibbleIsUpper(raw byte, value byte) bool {
	if value < 10 {
		return raw == '0'+value
	}
	return raw == 'A'+value-10
}

// utf8State is a single-pass UTF-8 validator. Feed every logical (decoded)
// byte in order; seqStart records the input offset where the current rune
// began so an error can point at the offending sequence.
type utf8State struct {
	need     int  // continuation bytes still expected
	min      byte // lowest legal second byte (overlong protection)
	max      byte // highest legal second byte (surrogate/range protection)
	seqStart int
	first    bool // whether the next byte is the 2nd byte of the rune
}

func (u *utf8State) feed(b byte, srcOff int) (bad bool, badOff int) {
	if b < 0x80 {
		if u.need > 0 {
			return true, u.seqStart // ASCII ends a multibyte rune early
		}
		return false, 0
	}
	if u.need == 0 {
		u.seqStart = srcOff
		switch {
		case b >= 0xC2 && b <= 0xDF:
			u.need, u.min, u.max, u.first = 1, 0x80, 0xBF, true
		case b == 0xE0:
			u.need, u.min, u.max, u.first = 2, 0xA0, 0xBF, true
		case b >= 0xE1 && b <= 0xEC:
			u.need, u.min, u.max, u.first = 2, 0x80, 0xBF, true
		case b == 0xED:
			u.need, u.min, u.max, u.first = 2, 0x80, 0x9F, true
		case b >= 0xEE && b <= 0xEF:
			u.need, u.min, u.max, u.first = 2, 0x80, 0xBF, true
		case b == 0xF0:
			u.need, u.min, u.max, u.first = 3, 0x90, 0xBF, true
		case b >= 0xF1 && b <= 0xF3:
			u.need, u.min, u.max, u.first = 3, 0x80, 0xBF, true
		case b == 0xF4:
			u.need, u.min, u.max, u.first = 3, 0x80, 0x8F, true
		default:
			return true, srcOff // 0x80-0xBF stray, 0xC0/0xC1 overlong, >0xF4
		}
		return false, 0
	}
	// Continuation byte. The first one must lie within [min,max].
	lo, hi := byte(0x80), byte(0xBF)
	if u.first { // this is the 2nd byte of the rune
		lo, hi = u.min, u.max
	}
	if b < lo || b > hi {
		return true, u.seqStart
	}
	u.first = false
	u.need--
	return false, 0
}

func (u *utf8State) finish() (bad bool, badOff int) {
	if u.need > 0 {
		return true, u.seqStart
	}
	return false, 0
}
