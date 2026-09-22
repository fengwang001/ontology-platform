package pct

const hexUpper = "0123456789ABCDEF"

func hexVal(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	default:
		return 0, false
	}
}

func appendEscaped(dst []byte, b byte) []byte {
	dst = append(dst, '%')
	dst = append(dst, hexUpper[b>>4], hexUpper[b&0x0F])
	return dst
}

// Normalize canonicalizes percent-escapes in in in one left-to-right pass.
// A byte accepted literally by safe is emitted literally; every other byte
// (raw or decoded from an escape) is emitted as an uppercase %XX escape.
// Malformed escapes and invalid UTF-8 produce an error and no result.
func Normalize(in string, safe func(byte) bool, scan ByteScanner) (string, error) {
	if scan != nil {
		scan(len(in))
	}
	out := make([]byte, 0, len(in))
	val := newValidator()
	var badOff int

	fail := func(kind string, off int) (string, error) {
		return "", &EscapeError{Kind: kind, Offset: off}
	}

	for i := 0; i < len(in); i++ {
		b := in[i]
		logicalOff := i
		if b == '%' {
			if i+2 >= len(in) {
				return fail(KindTruncated, i)
			}
			hi, ok1 := hexVal(in[i+1])
			lo, ok2 := hexVal(in[i+2])
			if !ok1 || !ok2 {
				if !ok1 {
					return fail(KindBadHex, i+1)
				}
				return fail(KindBadHex, i+2)
			}
			b = byte(hi<<4 | lo)
			logicalOff = i
			i += 2
		}
		if off, ok := val.feed(b, logicalOff); !ok {
			badOff = off
			goto badUTF
		}
		if safe(b) {
			out = append(out, b)
		} else {
			out = appendEscaped(out, b)
		}
	}
	if off, ok := val.end(); !ok {
		badOff = off
		goto badUTF
	}
	return string(out), nil

badUTF:
	return fail(KindUTF8, badOff)
}
