// Package eol recognizes mixed line endings (\r\n, lone \r, \n) across
// arbitrary chunk boundaries. It has no dependencies on other packages.
package eol

// Event is emitted by Scanner.Feed.
type Event uint8

const (
	// Byte is an ordinary byte; its value is passed to the callback.
	Byte Event = iota
	// Newline is a normalized line ending; origLen is 2 for \r\n, else 1.
	Newline
)

// Scanner is a single-use state machine. A trailing \r whose following byte
// has not been seen yet stays pending until the next Feed or Finish.
type Scanner struct {
	pendingCR bool
}

// NewScanner returns an empty scanner.
func NewScanner() *Scanner { return &Scanner{} }

// Feed consumes p. For every resolved token it calls emit(ev, v, origLen):
// for Byte, v is the byte value and origLen is 1; for Newline, v is '\n'
// and origLen is 1 (lone \r or \n) or 2 (\r\n). A trailing \r stays pending.
func (s *Scanner) Feed(p []byte, emit func(ev Event, v byte, origLen int)) {
	for i := 0; i < len(p); i++ {
		c := p[i]
		if s.pendingCR {
			s.pendingCR = false
			if c == '\n' {
				emit(Newline, '\n', 2)
				continue
			}
			emit(Newline, '\n', 1)
		}
		switch c {
		case '\n':
			emit(Newline, '\n', 1)
		case '\r':
			if i+1 < len(p) {
				if p[i+1] == '\n' {
					emit(Newline, '\n', 2)
					i++
				} else {
					emit(Newline, '\n', 1)
				}
			} else {
				s.pendingCR = true
			}
		default:
			emit(Byte, c, 1)
		}
	}
}

// Finish flushes a pending \r as a lone line ending. It must be called once
// when no more input is expected.
func (s *Scanner) Finish(emit func(ev Event, v byte, origLen int)) {
	if s.pendingCR {
		s.pendingCR = false
		emit(Newline, '\n', 1)
	}
}

// Pending reports whether a \r is awaiting its following byte.
func (s *Scanner) Pending() bool { return s.pendingCR }
