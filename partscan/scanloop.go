package partscan

// scanPreamble verifies that the stream begins with "--<boundary>\r\n".
// Bytes are matched one at a time via buf so a preamble split across chunks
// is handled. Any mismatch is a terminal ErrNoPreamble.
func (s *Scanner) scanPreamble(out [][]byte) ([][]byte, error) {
	matched := len(s.buf)
	if matched > len(s.preamble) {
		matched = len(s.preamble)
	}
	for i := 0; i < matched; i++ {
		if s.buf[i] != s.preamble[i] {
			return s.fail(ErrNoPreamble)
		}
	}

	// Keep waiting if the buffered bytes are only a prefix of the preamble.
	if len(s.buf) < len(s.preamble) {
		return out, nil
	}

	s.buf = s.buf[len(s.preamble):]
	s.pos = 0
	s.match.reset()
	s.phase = phasePart
	return out, nil
}

// scanPart scans buf for the part delimiter. Intermediate delimiters emit the
// part and keep the scanner in phasePart; the closing delimiter moves it to
// phaseDone. A trailing partial match, or a tentative full match whose
// distinguishing look-ahead bytes are not yet available, stays buffered.
func (s *Scanner) scanPart(out [][]byte) ([][]byte, error) {
	dlen := len(s.delimiter)
	for s.pos < len(s.buf) {
		if s.match.state == dlen {
			// A full delimiter is tentatively matched, ending at pos-1.
			// Look-ahead bytes start at pos. "held" means wait for more data.
			decision, held := s.peekSuffix()
			if held {
				break
			}
			if decision == suffixNeither {
				// Disprove with the first look-ahead byte and continue.
				s.match.failState(s.buf[s.pos])
				s.pos++
				continue
			}

			// Delimiter ends at pos-1; the two look-ahead bytes are at
			// [pos, pos+1). The delimiter therefore starts at pos-dlen.
			delimStart := s.pos - dlen
			part := make([]byte, delimStart)
			copy(part, s.buf[:delimStart])
			out = append(out, part)

			if decision == suffixClose {
				// Ignore any trailing bytes after the closer.
				s.buf = nil
				s.pos = 0
				s.phase = phaseDone
				return out, nil
			}

			// suffixSep: discard part + delimiter + "\r\n", keep scanning.
			s.buf = s.buf[delimStart+dlen+2:]
			s.pos = 0
			s.match.reset()
			continue
		}

		s.match.push(s.buf[s.pos])
		s.pos++
	}
	return out, nil
}

// peekSuffix classifies the two bytes after a tentative full match. The
// boolean is true while the buffered prefix could still become either a
// separator or a closer but cannot yet be disproven ("-", "\r", or nothing).
func (s *Scanner) peekSuffix() (suffixKind, bool) {
	if s.pos >= len(s.buf) {
		return suffixNeither, true
	}
	c1 := s.buf[s.pos]
	if s.pos+1 >= len(s.buf) {
		if c1 == '-' || c1 == '\r' {
			return suffixNeither, true
		}
		return suffixNeither, false
	}
	return classifySuffix(c1, s.buf[s.pos+1]), false
}

type suffixKind uint8

const (
	suffixNeither suffixKind = iota // cannot become separator or closer
	suffixSep                       // "\r\n" -> part separator
	suffixClose                     // "--" -> closing delimiter
)

// classifySuffix inspects the two bytes immediately following a delimiter.
func classifySuffix(c1, c2 byte) suffixKind {
	switch {
	case c1 == '-' && c2 == '-':
		return suffixClose
	case c1 == '\r' && c2 == '\n':
		return suffixSep
	default:
		return suffixNeither
	}
}
