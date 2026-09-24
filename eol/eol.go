// Package eol recognizes mixed line endings one byte at a time.
//
// A '\r' is undecided until the next byte: '\n' makes it CRLF (one
// ending), anything else (including another '\r' or EOF) makes it a
// lone CR (also one ending). The scanner is split-point agnostic: it
// carries only the pending-CR bit, so feeding bytes in arbitrary
// chunks yields identical events.
package eol

// Scanner turns a byte stream into normalized line-ending events.
// Zero value is ready to use.
type Scanner struct {
	cr bool
}

// Feed consumes one byte. extra reports that a previously pending
// '\r' just resolved as a lone line ending. lf reports that b itself
// completes a line ending (a lone '\n' or the '\n' of a CRLF pair).
// At most one of the return values is true.
func (s *Scanner) Feed(b byte) (extra, lf bool) {
	switch b {
	case '\r':
		if s.cr {
			extra = true
		}
		s.cr = true
	case '\n':
		s.cr = false
		lf = true
	default:
		if s.cr {
			extra = true
			s.cr = false
		}
	}
	return extra, lf
}

// Close resolves a pending '\r' at stream end, reporting one ending.
func (s *Scanner) Close() (extra bool) {
	if s.cr {
		s.cr = false
		return true
	}
	return false
}

// Pending reports whether a '\r' awaits the next byte.
func (s *Scanner) Pending() bool { return s.cr }
