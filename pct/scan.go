package pct

const hexUpper = "0123456789ABCDEF"

// Normalize performs one single left-to-right scan over s:
//   - '%XX' escapes are validated and their hex letters uppercased;
//   - escapes of unreserved characters (RFC 3986) are restored to literals;
//   - every other escape stays escaped (e.g. '%2F' must never become '/');
//   - the resulting byte stream is validated as UTF-8 in the same pass.
//
// changed reports whether the output differs from the input. On failure it
// returns an *Error with the exact byte offset and never a partial result.
// scanned is the number of input bytes consumed, so callers can prove the
// scan is linear in len(s) rather than quadratic.
func Normalize(s string) (out string, changed bool, scanned int, err error) {
	buf := make([]byte, 0, len(s))
	var st utf8State
	emit := func(b byte, srcOff int) error {
		if bad, off := st.feed(b, srcOff); bad {
			return &Error{Kind: KindInvalidUTF8, Offset: off}
		}
		buf = append(buf, b)
		return nil
	}
	for i := 0; i < len(s); {
		c := s[i]
		if c != '%' {
			if err := emit(c, i); err != nil {
				return "", false, i + 1, err
			}
			i++
			continue
		}
		if i+2 >= len(s) {
			return "", false, i, &Error{Kind: KindShortEscape, Offset: i}
		}
		hi, ok1 := fromHex(s[i+1])
		lo, ok2 := fromHex(s[i+2])
		if !ok1 || !ok2 {
			return "", false, i + 3, &Error{Kind: KindBadHex, Offset: i}
		}
		decoded := hi<<4 | lo
		isUnres := isUnreserved(decoded)
		if isUnres {
			if err := emit(decoded, i); err != nil {
				return "", false, i + 3, err
			}
		} else {
			// Preserve the escape; validate its decoded byte as UTF-8.
			if err := emit(decoded, i); err != nil {
				return "", false, i + 3, err
			}
			b := buf[len(buf)-1]
			buf = buf[:len(buf)-1]
			buf = append(buf, '%', hexUpper[b>>4], hexUpper[b&0x0F])
		}
		upperOK := nibbleIsUpper(s[i+1], hi) && nibbleIsUpper(s[i+2], lo)
		if isUnres || !upperOK {
			changed = true
		}
		i += 3
	}
	if bad, off := st.finish(); bad {
		return "", false, len(s), &Error{Kind: KindInvalidUTF8, Offset: off}
	}
	return string(buf), changed, len(s), nil
}
