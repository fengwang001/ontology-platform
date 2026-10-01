package ontology

import "unicode/utf8"

// decodeStatus is the result of decoding one code point from a byte
// prefix.
type decodeStatus int

const (
	statusComplete decodeStatus = iota
	statusInvalid
	// statusIncomplete means the bytes are a valid, but not yet
	// complete, prefix of a legal UTF-8 encoding.
	statusIncomplete
)

// decodeOne decodes exactly one UTF-8 code point at the start of p.
//
// For statusComplete it returns the rune, its total byte length and the
// number of bytes consumed (equal to the length). For statusIncomplete it
// returns the expected total length and the number of bytes currently
// present (consumed); the present bytes are all consistent with the
// expected encoding (continuation byte ranges included). statusInvalid
// means the byte at position 0 starts a sequence that cannot be the
// prefix of any legal UTF-8 encoding, including overlong encodings,
// surrogate code points and code points above U+10FFFF.
func decodeOne(p []byte) (r rune, total, consumed int, status decodeStatus) {
	b0 := p[0]
	switch {
	case b0 < 0x80:
		return rune(b0), 1, 1, statusComplete
	case b0 < 0xC2:
		// 0x80-0xBF stray continuation, 0xC0-0xC1 overlong 2-byte.
		return 0, 0, 0, statusInvalid
	case b0 <= 0xDF:
		return decodeMulti(p, 2, func(b []byte) (rune, bool) {
			r := rune(b0&0x1F)<<6 | rune(b[1]&0x3F)
			return r, true
		})
	case b0 <= 0xEF:
		return decodeMulti(p, 3, func(b []byte) (rune, bool) {
			r := rune(b0&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F)
			switch b0 {
			case 0xE0:
				if b[1] < 0xA0 { // overlong
					return 0, false
				}
			case 0xED:
				if b[1] > 0x9F { // surrogate U+D800-U+DFFF
					return 0, false
				}
			}
			return r, true
		})
	case b0 <= 0xF4:
		return decodeMulti(p, 4, func(b []byte) (rune, bool) {
			r := rune(b0&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F)
			switch b0 {
			case 0xF0:
				if b[1] < 0x90 { // overlong
					return 0, false
				}
			case 0xF4:
				if b[1] > 0x8F { // above U+10FFFF
					return 0, false
				}
			}
			return r, true
		})
	default:
		return 0, 0, 0, statusInvalid
	}
}

func decodeMulti(p []byte, size int, assemble func([]byte) (rune, bool)) (rune, int, int, decodeStatus) {
	for i := 1; i < size; i++ {
		if i >= len(p) {
			return 0, size, len(p), statusIncomplete
		}
		b := p[i]
		lo, hi := byte(0x80), byte(0xBF)
		switch {
		case size == 3 && i == 1 && p[0] == 0xE0:
			lo = 0xA0
		case size == 3 && i == 1 && p[0] == 0xED:
			hi = 0x9F
		case size == 4 && i == 1 && p[0] == 0xF0:
			lo = 0x90
		case size == 4 && i == 1 && p[0] == 0xF4:
			hi = 0x8F
		}
		if b < lo || b > hi {
			return 0, 0, 0, statusInvalid
		}
	}
	r, ok := assemble(p[:size])
	if !ok {
		return 0, 0, 0, statusInvalid
	}
	return r, size, size, statusComplete
}

// mapChar applies the per-source-character filter. It returns the zero or
// more output bytes a source character produces; a nil result means the
// character is deleted (it neither contributes token bytes nor acts as a
// separator).
func mapChar(r rune) []byte {
	switch {
	case r >= 0x0300 && r <= 0x036F:
		return nil
	case r == 'ß':
		return []byte("ss")
	case r == 'æ' || r == 'Æ':
		return []byte("ae")
	case r == 'œ' || r == 'Œ':
		return []byte("oe")
	case r == '\uFB01':
		return []byte("fi")
	case r == '\uFB02':
		return []byte("fl")
	case r >= 'A' && r <= 'Z':
		return []byte{byte(r + ('a' - 'A'))}
	default:
		var buf [utf8.UTFMax]byte
		n := utf8.EncodeRune(buf[:], r)
		return buf[:n]
	}
}

// isTokenByte reports whether an output byte belongs to [a-z0-9].
func isTokenByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}
