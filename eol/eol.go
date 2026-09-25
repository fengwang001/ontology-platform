// Package eol recognizes mixed line endings (\r\n, lone \r, \n) byte by byte,
// including the undecided state of a trailing \r across chunk boundaries.
package eol

const (
	CR byte = '\r'
	LF byte = '\n'
)

// Event classifies an input position.
type Event uint8

const (
	// Content: the byte is not part of a line ending.
	Content Event = iota
	// Newline: exactly one normalized line ending starts at this byte.
	Newline
	// PendingCR: a \r seen, whose fate waits for the next byte.
	PendingCR
)

// Scanner is a single-byte-at-a-time line-ending classifier.
// A \r followed by \n is one line ending; \r\r\n is two (\r then \r\n).
type Scanner struct {
	pending bool
}

// Feed2 classifies b when a \r may be pending: first is the event for the
// previously pending \r (Content when none), second is the event for b.
// With input \r\n the pair resolves to (Newline, Content): one ending.
func (s *Scanner) Feed2(b byte) (first, second Event) {
	if s.pending {
		s.pending = false
		if b == LF {
			return Newline, Content // \r\n is one ending; \n contributes nothing
		}
		first = Newline // lone \r resolves
	}
	if b == CR {
		s.pending = true
		return first, PendingCR
	}
	if b == LF {
		return first, Newline
	}
	return first, Content
}

// Flush resolves a trailing \r as a lone line ending. Returns true if a
// Newline must be emitted for it.
func (s *Scanner) Flush() bool {
	if s.pending {
		s.pending = false
		return true
	}
	return false
}

// Pending reports whether a \r awaits the next byte.
func (s *Scanner) Pending() bool { return s.pending }
