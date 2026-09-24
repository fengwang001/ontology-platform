// Package linescan incrementally splits byte chunks into logical lines.
// It treats \n, \r\n and \r as equivalent line terminators, buffers
// partial lines across Feed calls and swallows a leading UTF-8 BOM.
package linescan

import "errors"

// ErrLineTooLong is returned when a line exceeds the configured maximum.
var ErrLineTooLong = errors.New("linescan: line exceeds maximum length")

const bom = "\xef\xbb\xbf"

// Scanner splits byte chunks into lines. Its results do not depend on
// how the input bytes were chunked.
type Scanner struct {
	buf     []byte // buffered partial line
	prefix  []byte // undecided leading bytes (possible BOM)
	afterCR bool   // last consumed byte was '\r'
	bomDone bool   // leading BOM decision made
	max     int    // max line length in bytes; <=0 means unlimited
}

// New returns a Scanner rejecting lines longer than maxLine bytes.
func New(maxLine int) *Scanner { return &Scanner{max: maxLine} }

// Clone returns a deep copy of the scanner state.
func (s *Scanner) Clone() *Scanner {
	c := *s
	c.buf = append([]byte(nil), s.buf...)
	c.prefix = append([]byte(nil), s.prefix...)
	return c
}

// Feed consumes chunk and returns the lines completed by it, without
// terminators. On error the scanner state is left unchanged.
func (s *Scanner) Feed(chunk []byte) ([]string, error) {
	prefix, bomDone := s.prefix, s.bomDone
	if !bomDone {
		prefix = append(prefix, chunk...)
		n := 0
		for n < len(prefix) && n < len(bom) && prefix[n] == bom[n] {
			n++
		}
		if n == len(prefix) && n < len(bom) {
			s.prefix = prefix // still a prefix of the BOM; wait for more
			return nil, nil
		}
		bomDone = true
		if n == len(bom) {
			prefix = prefix[len(bom):]
		}
		chunk, prefix = prefix, nil
	}
	buf, afterCR := s.buf, s.afterCR
	var lines []string
	seg := 0
	for i, c := range chunk {
		if afterCR {
			afterCR = false
			if c == '\n' { // tail of a "\r\n": one terminator, not two
				seg = i + 1
				continue
			}
		}
		if c != '\n' && c != '\r' {
			continue
		}
		if s.max > 0 && len(buf)+i-seg > s.max {
			return nil, ErrLineTooLong
		}
		lines = append(lines, string(buf)+string(chunk[seg:i]))
		buf = nil
		seg = i + 1
		afterCR = c == '\r'
	}
	rest := chunk[seg:]
	if s.max > 0 && len(buf)+len(rest) > s.max {
		return nil, ErrLineTooLong
	}
	s.buf = append(buf, rest...)
	s.afterCR = afterCR
	s.prefix, s.bomDone = prefix, bomDone
	return lines, nil
}

// Flush returns the buffered partial line, if any, and resets the
// scanner. A trailing '\r' already emitted its line, so it leaves
// nothing to flush; undecided leading bytes are real data by now.
func (s *Scanner) Flush() (string, bool) {
	data := append(s.prefix, s.buf...)
	line, ok := string(data), len(data) > 0
	s.buf, s.prefix, s.afterCR, s.bomDone = nil, nil, false, true
	return line, ok
}
