package filename

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func encodeDangerous(name string) string {
	var b strings.Builder
	dots := 0
	for i := 0; i < len(name); {
		if name[i] == '.' {
			dots++
			if dots == 1 {
				b.WriteByte('.')
			} else {
				b.WriteString("%2E")
			}
			i++
			continue
		}
		dots = 0
		r, size := utf8.DecodeRuneInString(name[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, "%%%02X", name[i])
		case r == '/' || r == '\\' || r == '%' || r == 0 || r < 0x20 || r == 0x7f:
			writeRunePercent(&b, r)
		default:
			b.WriteRune(r)
		}
		i += size
	}
	return b.String()
}

func percentDecode(raw string) []byte {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		if raw[i] == '%' && i+2 < len(raw) && isHex(raw[i+1]) && isHex(raw[i+2]) {
			out = append(out, hexValue(raw[i+1])<<4|hexValue(raw[i+2]))
			i += 3
			continue
		}
		out = append(out, raw[i])
		i++
	}
	return out
}

func writeRunePercent(b *strings.Builder, r rune) {
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	for _, c := range buf[:n] {
		fmt.Fprintf(b, "%%%02X", c)
	}
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
