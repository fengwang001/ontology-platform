package chunked

// feedTrailers consumes trailer lines after the zero-size chunk. Each line
// must end with CRLF; the section ends with an empty line (CRLF). Trailer
// content is discarded (only its count and well-formedness matter) and never
// enters the body.
func (d *Decoder) feedTrailers(in []byte) (int, error) {
	for i := 0; i < len(in); i++ {
		c := in[i]
		off := d.stream + i
		switch {
		case d.tCR:
			// Previous byte was CR: must be LF.
			if c != '\n' {
				return i, &Error{Kind: KindBadTrailer, Offset: off}
			}
			d.tCR = false
			if d.tEmpty {
				d.ph = phComplete
				return i + 1, nil
			}
			d.tEmpty = true // starting a fresh line, initially empty
		case c == '\r':
			d.tCR = true
		case c == '\n':
			// A bare LF is never a valid line ending.
			return i, &Error{Kind: KindBadTrailer, Offset: off}
		default:
			if d.tEmpty {
				d.tCount++
				if d.limits.MaxTrailers > 0 && d.tCount > d.limits.MaxTrailers {
					return i, &Error{Kind: KindTooManyTrailers, Offset: off}
				}
				if !isTrailerNameByte(c) {
					return i, &Error{Kind: KindBadTrailer, Offset: off}
				}
				d.tEmpty = false
			}
		}
	}
	return len(in), nil
}

func isTrailerNameByte(c byte) bool {
	// RFC 7230 token characters (first trailer byte).
	switch c {
	case '"', '(', ')', ',', '/', ':', ';', '<', '=', '>', '?', '@',
		'[', '\\', ']', '{', '}', ' ', '\t', 0x7f:
		return false
	}
	return c > 0x20
}
