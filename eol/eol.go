// Package eol identifies line endings across stream chunk boundaries.
// It recognizes "\r\n", lone "\r" and "\n", keeping a trailing "\r"
// pending until the next byte decides whether it starts a CRLF.
package eol

const (
	CR byte = '\r'
	LF byte = '\n'
)

// Scanner is a single-use, non-concurrent byte stream classifier.
// Zero value is not ready; use New.
type Scanner struct {
	cr bool // a '\r' whose following byte has not been seen
}

func New() *Scanner { return &Scanner{} }

// Pending reports whether a trailing '\r' awaits the next byte.
func (s *Scanner) Pending() bool { return s.cr }

// Feed consumes the byte at original offset off. When a line ending becomes
// known it returns its original start offset and length:
// length 2 means "\r\n", length 1 means lone "\r" or "\n", length 0 means
// the byte is ordinary content (and no earlier pending break was flushed).
func (s *Scanner) Feed(off int, b byte) (start, length int) {
	switch {
	case b == LF:
		if s.cr {
			s.cr = false
			return off - 1, 2
		}
		return off, 1
	case b == CR:
		if s.cr { // previous '\r' is a lone line ending
			s.cr = true
			return off - 1, 1
		}
		s.cr = true
		return 0, 0
	default:
		if s.cr {
			s.cr = false
			return off - 1, 1
		}
		return 0, 0
	}
}

// Flush must be called at stream end (off == total input length). A pending
// '\r' is a lone line ending; otherwise nothing is reported.
func (s *Scanner) Flush(off int) (start, length int) {
	if s.cr {
		s.cr = false
		return off - 1, 1
	}
	return 0, 0
}
