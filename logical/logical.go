package logical

import (
	"io"
)

type Part struct {
	Data []byte
	Line int
	Col  int
}

type Line struct {
	Parts   []Part
	Comment bool
}

type Scanner struct {
	data    []byte
	pos     int
	line    int
	current Line
	checked int64
}

func NewScanner(r io.Reader) (*Scanner, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return &Scanner{data: data, line: 1}, nil
}

func (s *Scanner) Checked() int64 { return s.checked }

func (s *Scanner) Line() Line { return s.current }

func (s *Scanner) Next() (*Line, bool) {
	raw, line, ok := s.physical()
	if !ok {
		return nil, false
	}
	parts := []Part{{Data: raw, Line: line, Col: 1}}
	for s.oddContinuation(raw) {
		raw = raw[:len(raw)-1]
		parts[len(parts)-1].Data = raw
		next, nextLine, more := s.physical()
		if !more {
			break
		}
		skip := 0
		for skip < len(next) && isSpace(next[skip]) {
			skip++
			s.checked++
		}
		if skip == len(next) {
			break
		}
		raw = next[skip:]
		parts = append(parts, Part{Data: raw, Line: nextLine, Col: skip + 1})
	}
	s.current = Line{Parts: parts, Comment: s.isComment(parts)}
	return &s.current, true
}

func (s *Scanner) physical() ([]byte, int, bool) {
	if s.pos >= len(s.data) {
		return nil, 0, false
	}
	start, line := s.pos, s.line
	for s.pos < len(s.data) && s.data[s.pos] != '\n' && s.data[s.pos] != '\r' {
		s.checked++
		s.pos++
	}
	raw := s.data[start:s.pos]
	if s.pos < len(s.data) {
		if s.data[s.pos] == '\r' {
			s.pos++
			s.checked++
		}
		if s.pos < len(s.data) && s.data[s.pos] == '\n' {
			s.pos++
			s.checked++
		}
		s.line++
	}
	return raw, line, true
}

func (s *Scanner) oddContinuation(raw []byte) bool {
	slashes := 0
	for len(raw) > slashes && raw[len(raw)-1-slashes] == '\\' {
		slashes++
		s.checked++
	}
	return slashes%2 == 1
}

func (s *Scanner) isComment(parts []Part) bool {
	for _, part := range parts {
		for _, b := range part.Data {
			if !isSpace(b) {
				return b == '#' || b == '!'
			}
			s.checked++
		}
	}
	return false
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\f'
}
