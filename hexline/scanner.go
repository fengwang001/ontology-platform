package hexline

import "errors"

var (
	// ErrLineTooLong indicates a line exceeded the configured maximum
	// length. It is reported as soon as the (maxLen+1)-th byte arrives.
	ErrLineTooLong = errors.New("hexline: line exceeds maximum length")
	// ErrBareLF indicates a LF that was not preceded by a CR.
	ErrBareLF = errors.New("hexline: line feed without preceding carriage return")
	// ErrBareCR indicates a CR that was not followed by a LF.
	ErrBareCR = errors.New("hexline: carriage return not followed by line feed")
)

// Scanner incrementally accumulates one CRLF-terminated line from
// arbitrarily fragmented input. It is used for chunk-size lines and
// trailer lines. A Scanner is not safe for concurrent use.
type Scanner struct {
	maxLen int
	buf    []byte
	cr     bool
}

// NewScanner returns a Scanner rejecting lines longer than maxLen
// bytes (the terminating CRLF is not counted).
func NewScanner(maxLen int) *Scanner {
	return &Scanner{maxLen: maxLen}
}

// PendingCR reports whether the last consumed byte was a CR whose LF
// has not yet arrived.
func (s *Scanner) PendingCR() bool {
	return s.cr
}

// Buffered reports how many line bytes have been accumulated so far.
func (s *Scanner) Buffered() int {
	return len(s.buf)
}

// Write feeds p into the Scanner.
//
// It returns the completed line (without CRLF), the number of bytes
// consumed from p, whether a line was completed, and any error. On
// error the returned consumed count includes the offending byte. After
// completion or error the Scanner is reset and may be reused.
func (s *Scanner) Write(p []byte) (line []byte, consumed int, complete bool, err error) {
	for i, b := range p {
		if s.cr {
			s.cr = false
			if b != '\n' {
				s.buf = nil
				return nil, i + 1, false, ErrBareCR
			}
			line = s.buf
			s.buf = nil
			return line, i + 1, true, nil
		}
		switch b {
		case '\r':
			s.cr = true
		case '\n':
			s.buf = nil
			return nil, i + 1, false, ErrBareLF
		default:
			s.buf = append(s.buf, b)
			if len(s.buf) > s.maxLen {
				s.buf = nil
				return nil, i + 1, false, ErrLineTooLong
			}
		}
	}
	return nil, len(p), false, nil
}
