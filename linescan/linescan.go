// Package linescan incrementally splits arbitrary byte chunks into
// logical lines, treating \n, \r\n and \r as equivalent line endings.
package linescan

import "errors"

// ErrLineTooLong is returned when a single line exceeds the configured
// maximum number of bytes.
var ErrLineTooLong = errors.New("linescan: line exceeds maximum length")

var bom = []byte{0xEF, 0xBB, 0xBF}

// Scanner holds the cross-chunk splitting state: the half-line buffer,
// a one-bit "\r pending" state, and the undecided stream-head BOM state.
type Scanner struct {
	maxLine int
	buf     []byte
	afterCR bool
	head    []byte
	decided bool
}

// New returns a Scanner rejecting lines longer than maxLine bytes
// (maxLine <= 0 means unlimited).
func New(maxLine int) *Scanner { return &Scanner{maxLine: maxLine} }

// Reset clears all splitting state, as for a fresh connection.
func (s *Scanner) Reset() {
	s.buf = s.buf[:0]
	s.afterCR = false
	s.head = nil
	s.decided = false
}

// Feed consumes one chunk, calling emit once per complete line.
// The slice passed to emit is reused after emit returns.
func (s *Scanner) Feed(chunk []byte, emit func([]byte) error) error {
	for _, b := range chunk {
		if err := s.step(b, emit); err != nil {
			return err
		}
	}
	return nil
}

// Flush emits a trailing unterminated line, if any. A pending "\r"
// needs no action: its line was already emitted when the "\r" arrived.
func (s *Scanner) Flush(emit func([]byte) error) error {
	s.afterCR = false
	if len(s.buf) == 0 {
		return nil
	}
	line := s.buf
	s.buf = s.buf[:0]
	return emit(line)
}

func (s *Scanner) step(b byte, emit func([]byte) error) error {
	if !s.decided {
		if b == bom[len(s.head)] {
			s.head = append(s.head, b)
			if len(s.head) == len(bom) {
				s.decided = true
				s.head = nil // stream-head BOM eaten
			}
			return nil
		}
		s.decided = true
		head := s.head
		s.head = nil
		for _, h := range head { // not a BOM: replay buffered head bytes
			if err := s.byte_(h, emit); err != nil {
				return err
			}
		}
	}
	return s.byte_(b, emit)
}

func (s *Scanner) byte_(b byte, emit func([]byte) error) error {
	if s.afterCR {
		s.afterCR = false
		if b == '\n' { // second half of a "\r\n": swallow
			return nil
		}
	}
	switch b {
	case '\r':
		if err := emit(s.buf); err != nil {
			return err
		}
		s.buf = s.buf[:0]
		s.afterCR = true
	case '\n':
		if err := emit(s.buf); err != nil {
			return err
		}
		s.buf = s.buf[:0]
	default:
		if s.maxLine > 0 && len(s.buf) >= s.maxLine {
			return ErrLineTooLong
		}
		s.buf = append(s.buf, b)
	}
	return nil
}
