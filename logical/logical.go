// Package logical turns the physical lines of a Java .properties stream into
// logical lines: it drops blank/comment lines, joins backslash continuations
// (only an odd number of trailing backslashes continues) and strips the leading
// whitespace of a continuation. It has no dependency on other packages.
package logical

import (
	"bufio"
	"io"
)

// Pos is the 1-based physical line and column (rune column) of a source rune.
type Pos struct{ Line, Col int }

// Line is one assembled logical line. Pos[i] is the source position of Text[i].
type Line struct {
	Text []rune
	Pos  []Pos
}

// Scanner reads logical lines from a physical stream.
type Scanner struct {
	r       *bufio.Reader
	checked int64 // number of input bytes examined
	line    int
	col     int
	cr      bool // previous rune was '\r' awaiting a possible '\n'

	text []rune
	pos  []Pos

	lead    bool // skipping leading whitespace of a fresh physical line
	cont    bool // continuation: leading WS stripped, '#' stays data
	comment bool // current physical line is a comment
	slashes int  // consecutive trailing backslashes in data
}

// NewScanner returns a Scanner over r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReader(r), line: 1, lead: true}
}

// Checks reports how many physical input bytes have been examined.
func (s *Scanner) Checks() int64 { return s.checked }

func isWS(ch rune) bool { return ch == ' ' || ch == '\t' || ch == '\f' }

// Next returns the next logical line, or io.EOF when none remain.
func (s *Scanner) Next() (*Line, error) {
	s.text, s.pos = s.text[:0], s.pos[:0]
	s.lead, s.cont, s.comment, s.slashes = true, false, false, 0
	for {
		ch, size, err := s.r.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if ch == '\n' && s.cr {
			s.cr = false // swallow the LF of a CRLF pair
			continue
		}
		s.cr = ch == '\r'
		s.checked += int64(size)
		if ch == '\r' {
			ch = '\n'
		}
		s.col++
		if ch == '\n' {
			continued := !s.comment && s.slashes%2 == 1
			s.line++
			s.col = 0
			s.lead, s.comment, s.slashes = true, false, 0
			if continued {
				s.cont = true // survives blank/whitespace-only physical rows
			} else if len(s.text) > 0 {
				out := &Line{Text: append([]rune(nil), s.text...), Pos: append([]Pos(nil), s.pos...)}
				s.text, s.pos = s.text[:0], s.pos[:0]
				s.cont = false
				return out, nil
			}
			continue
		}
		if s.lead {
			if isWS(ch) {
				continue
			}
			if !s.cont && (ch == '#' || ch == '!') {
				s.comment = true
				continue
			}
			s.lead = false
		}
		if s.comment {
			continue
		}
		if ch == '\\' {
			s.slashes++
		} else {
			s.slashes = 0
		}
		s.text = append(s.text, ch)
		s.pos = append(s.pos, Pos{Line: s.line, Col: s.col})
	}
	if len(s.text) > 0 {
		return &Line{Text: append([]rune(nil), s.text...), Pos: append([]Pos(nil), s.pos...)}, nil
	}
	return nil, io.EOF
}
