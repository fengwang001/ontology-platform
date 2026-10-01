package snippet

// utf8Boundaries returns a boolean slice where boundary[i] is true exactly
// when offset i is a rune boundary of text: offset 0, len(text), and every
// byte that does not continue a UTF-8 sequence. Invalid encodings make the
// boundary map unusable for validation, so callers must first verify text
// with a strict UTF-8 decode.
func utf8Boundaries(text string) []bool {
	boundary := make([]bool, len(text)+1)
	boundary[0] = true
	for i := 0; i < len(text); {
		boundary[i] = true
		size := utf8RuneSize(text[i])
		if size < 1 || size > 4 || i+size > len(text) {
			i++
			continue
		}
		i += size
	}
	boundary[len(text)] = true
	return boundary
}

// utf8RuneSize returns the rune length encoded by a UTF-8 leading byte, or 0
// for a continuation byte.
func utf8RuneSize(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b>>5 == 0b110:
		return 2
	case b>>4 == 0b1110:
		return 3
	case b>>3 == 0b11110:
		return 4
	default:
		return 0
	}
}
