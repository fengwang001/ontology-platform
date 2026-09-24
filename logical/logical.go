package logical

import (
	"bufio"
	"io"
	"unicode"
	"unicode/utf8"
)

type Position struct{ Line, Column int }

type Segment struct {
	rune  rune
	width int
	pos   Position
}

type Line struct{ segments []Segment }

type Scanner struct {
	r        *bufio.Reader
	checked  uint64
	segments []Segment
	lineNo   int
	col      int
	start    bool
	indent   bool
	comment  bool
	escaped  bool
	content  bool
}

type countedReader struct {
	r io.Reader
	n *uint64
}

func (r countedReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	*r.n += uint64(n)
	return n, err
}

func NewScanner(r io.Reader) *Scanner {
	s := &Scanner{lineNo: 1, start: true}
	s.r = bufio.NewReader(countedReader{r: r, n: &s.checked})
	return s
}

func (s *Scanner) Next() (Line, bool) {
	for {
		r, size, ok := s.readRune()
		if !ok {
			if !s.content {
				return Line{}, false
			}
			out := s.finishPending()
			s.reset()
			return out, true
		}
		if r == '\n' || r == '\r' {
			if r == '\r' && s.peekByte() == '\n' {
				s.readByte()
			}
			if s.escaped {
				s.escaped, s.indent = false, true
			} else if s.content {
				out := s.finishPending()
				s.reset()
				return out, true
			} else {
				s.resetState()
			}
			s.lineNo++
			s.col = 0
			continue
		}
		if s.comment {
			s.escaped = r == '\\' && !s.escaped
			continue
		}
		if s.start && (r == '#' || r == '!') {
			s.comment, s.escaped = true, false
			continue
		}
		if (s.start || s.indent) && unicode.IsSpace(r) {
			continue
		}
		s.segments = append(s.segments, Segment{r, size, Position{s.lineNo, s.col}})
		s.content, s.start, s.indent = true, false, false
		s.escaped = r == '\\' && !s.escaped
	}
}

func (s *Scanner) BytesChecked() uint64 { return s.checked }
func (l Line) Len() int                 { return len(l.segments) }
func (l Line) RuneAt(i int) rune        { return l.segments[i].rune }
func (l Line) Position(i int) Position  { return l.segments[i].pos }

func (s *Scanner) finishPending() Line {
	if s.escaped && len(s.segments) > 0 {
		s.segments = s.segments[:len(s.segments)-1]
	}
	return Line{s.segments}
}

func (s *Scanner) reset() {
	s.segments = nil
	s.resetState()
}

func (s *Scanner) resetState() {
	s.start, s.indent, s.comment, s.escaped, s.content = true, false, false, false, false
}

func (s *Scanner) readRune() (rune, int, bool) {
	first := s.readByte()
	if first < 0 {
		return 0, 0, false
	}
	if first < utf8.RuneSelf {
		return rune(first), 1, true
	}
	if err := s.r.UnreadByte(); err != nil {
		return utf8.RuneError, 1, true
	}
	r, size, _ := s.r.ReadRune()
	if r == utf8.RuneError {
		return r, 1, true
	}
	s.col += size - 1
	return r, size, true
}

func (s *Scanner) readByte() int {
	b, err := s.r.ReadByte()
	if err != nil {
		return -1
	}
	s.col++
	return int(b)
}

func (s *Scanner) peekByte() byte {
	b, err := s.r.Peek(1)
	if err != nil {
		return 0
	}
	return b[0]
}
