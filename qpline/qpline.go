// Package qpline implements single-line Quoted-Printable (RFC 2045)
// encoding decisions: which bytes need escaping, trailing whitespace
// handling, and soft line break insertion. It has no dependencies.
package qpline

const MaxLineLen = 76 // hard cap per RFC 2045, excluding the trailing CRLF

// Literal reports whether b is emitted unchanged: printable ASCII
// 33..126 except '=', plus space and tab.
func Literal(b byte) bool {
	switch {
	case b == '=':
		return false
	case b == ' ' || b == '\t':
		return true
	default:
		return b >= 33 && b <= 126
	}
}

// Token returns the encoded form of a single input byte: either the
// byte itself (length 1) or an =XX escape (length 3, uppercase hex).
func Token(b byte) []byte {
	if Literal(b) {
		return []byte{b}
	}
	return []byte{'=', hexDigits[b>>4], hexDigits[b&0xF]}
}

const hexDigits = "0123456789ABCDEF"

// EncodeLine encodes one logical line (no newline bytes) and inserts
// soft breaks ("=\r\n") so no emitted line exceeds MaxLineLen characters.
// The soft-break '=' counts toward MaxLineLen; an =XX token is never split.
// When trailingWS is true, every run of space/tab at the end of the line
// is escaped (=20/=09); this is required when the line precedes a hard
// newline or end of input. A literal space immediately before a soft
// break is NOT escaped: a soft break is not a hard newline and the
// decoder removes only the "=\r\n" sequence, keeping the space.
func EncodeLine(line []byte, trailingWS bool) []byte {
	trailing := 0
	if trailingWS {
		for trailing < len(line) {
			b := line[len(line)-1-trailing]
			if b != ' ' && b != '\t' {
				break
			}
			trailing++
		}
	}

	out := make([]byte, 0, len(line)+len(line)/25)
	col := 0
	for i := 0; i < len(line); i++ {
		b := line[i]
		escape := !Literal(b) || (trailing > 0 && i >= len(line)-trailing)
		size := 1
		if escape {
			size = 3
		}
		if col+size > MaxLineLen || col == MaxLineLen && i < len(line) {
			out = append(out, '=', '\r', '\n')
			col = 0
		}
		if escape {
			out = append(out, '=', hexDigits[b>>4], hexDigits[b&0xF])
		} else {
			out = append(out, b)
		}
		col += size
	}
	return out
}
