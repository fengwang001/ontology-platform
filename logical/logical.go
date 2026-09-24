// Package logical assembles physical lines into logical lines following the
// line-continuation rules of java.util.Properties.
package logical

import (
	"bufio"
	"io"
)

// pos is the 1-based physical line and byte column of one logical byte.
type pos struct{ line, col int }

// Line is one assembled logical line.
type Line struct {
	text []byte
	pos  []pos
}

// Scanner yields logical lines in java.util.Properties continuation semantics.
type Scanner struct {
	br      *bufio.Reader
	checked int64
	physLn  int // current physical line number, 1-based
	physCol int // current physical column, 1-based (byte based)
}

// NewScanner returns a Scanner over r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{br: bufio.NewReader(r), physLn: 1, physCol: 1}
}

// Checked reports the total number of input bytes examined so far. Each input
// byte is examined at most twice (once during read, once during assembly).
func (s *Scanner) Checked() int64 { return s.checked }

func (s *Scanner) readByte() (byte, error) {
	c, err := s.br.ReadByte()
	if err == nil {
		s.checked++
		if c == '\n' {
			s.physLn++
			s.physCol = 1
		} else {
			s.physCol++
		}
	}
	return c, err
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// Next returns the next logical line, or (nil, nil) at clean EOF.
func (s *Scanner) Next() (*Line, error) {
	var text []byte
	var where []pos
	seekStart := true // skip leading whitespace of the logical line
	for {
		c, err := s.readByte()
		if err != nil {
			if err == io.EOF {
				if len(text) == 0 && seekStart {
					return nil, nil
				}
				return &Line{text: text, pos: where}, nil
			}
			return nil, err
		}
		if c == '\n' || c == '\r' {
			if c == '\r' {
				if nb, e := s.br.Peek(1); e == nil && nb[0] == '\n' {
					_, _ = s.readByte() // consume the LF of CRLF
				}
			}
			trailing := 0
			for i := len(text) - 1; i >= 0 && text[i] == '\\'; i-- {
				trailing++
			}
			if trailing%2 == 1 {
				text = text[:len(text)-1]
				where = where[:len(where)-1]
				seekStart = true // strip leading ws of the next physical line
				continue
			}
			return &Line{text: text, pos: where}, nil
		}
		if seekStart && isWS(c) {
			continue
		}
		seekStart = false
		text = append(text, c)
		where = append(where, pos{line: s.physLn, col: s.physCol - 1})
	}
}

// Text returns the assembled logical line bytes.
func (l *Line) Text() []byte { return l.text }

// IsComment reports whether the logical line is a comment or blank line.
func (l *Line) IsComment() bool {
	return len(l.text) == 0 || l.text[0] == '#' || l.text[0] == '!'
}

// PhysicalPos maps a logical byte offset to its 1-based physical line/column.
func (l *Line) PhysicalPos(offset int) (line, column int) {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(l.pos) {
		offset = len(l.pos) - 1
	}
	return l.pos[offset].line, l.pos[offset].col
}
