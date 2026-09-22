package pct

import (
	"strings"
	"unicode/utf8"
)

const upperHex = "0123456789ABCDEF"

// IsUnreserved reports whether b is an RFC 3986 unreserved character.
// Only these may have their redundant escapes folded back to literals.
func IsUnreserved(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '.', b == '_', b == '~':
		return true
	}
	return false
}

func hexVal(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

// Normalize scans s exactly once and returns its canonical form:
// hex letters of escapes are uppercased, escapes of unreserved bytes are
// folded to the literal byte, escapes of reserved bytes are kept.
// Any malformed escape or invalid decoded UTF-8 yields an *Error and no
// partial result.
func Normalize(s string) (string, error) {
	if !strings.Contains(s, "%") && utf8.ValidString(s) {
		return s, nil
	}
	var out strings.Builder
	out.Grow(len(s))
	dec := make([]byte, 0, len(s)) // fully decoded stream, for UTF-8 check
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			out.WriteByte(c)
			dec = append(dec, c)
			continue
		}
		if i+2 >= len(s) {
			return "", &Error{Kind: KindTruncated, Offset: i}
		}
		hi, ok1 := hexVal(s[i+1])
		lo, ok2 := hexVal(s[i+2])
		if !ok1 || !ok2 {
			off := i + 1
			if ok1 {
				off = i + 2
			}
			return "", &Error{Kind: KindBadHex, Offset: off}
		}
		v := byte(hi<<4 | lo)
		if IsUnreserved(v) {
			out.WriteByte(v)
		} else {
			out.WriteByte('%')
			out.WriteByte(upperHex[hi])
			out.WriteByte(upperHex[lo])
		}
		dec = append(dec, v)
		i += 2
	}
	if !utf8.Valid(dec) {
		return "", &Error{Kind: KindBadUTF8, Offset: utf8ErrOffset(s, dec)}
	}
	return out.String(), nil
}

// Decode fully decodes every escape in s. Single pass, strict errors.
func Decode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		if i+2 >= len(s) {
			return nil, &Error{Kind: KindTruncated, Offset: i}
		}
		hi, ok1 := hexVal(s[i+1])
		lo, ok2 := hexVal(s[i+2])
		if !ok1 || !ok2 {
			off := i + 1
			if ok1 {
				off = i + 2
			}
			return nil, &Error{Kind: KindBadHex, Offset: off}
		}
		out = append(out, byte(hi<<4|lo))
		i += 2
	}
	if !utf8.Valid(out) {
		return nil, &Error{Kind: KindBadUTF8, Offset: utf8ErrOffset(s, out)}
	}
	return out, nil
}

// utf8ErrOffset maps the first invalid UTF-8 position in the decoded
// stream back to a byte offset in the original (still encoded) input.
// Only used on the error path, so the happy path stays single-pass.
func utf8ErrOffset(input string, dec []byte) int {
	bad := 0
	for bad < len(dec) {
		_, size := utf8.DecodeRune(dec[bad:])
		if size == 1 && dec[bad] >= utf8.RuneSelf {
			break
		}
		bad += size
	}
	decPos := 0
	for i := 0; i < len(input); i++ {
		if decPos >= bad {
			return i
		}
		if input[i] == '%' && i+2 < len(input) {
			i += 2
		}
		decPos++
	}
	return len(input)
}
