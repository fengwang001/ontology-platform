// Package logical folds physical lines of a Java .properties stream into
// logical lines: comments, blank lines, and backslash line continuation.
package logical

import (
	"bufio"
	"io"
)

// Line is one logical line. Text omits leading whitespace of the logical line
// and of every physical line spliced in by continuation.
type Line struct {
	Text      string
	PhysLine  int // 1-based physical line where the logical line starts
	col       []int
	phys      []int
}

// StartLine reports the 1-based physical line number where the line begins.
func (l *Line) StartLine() int { return l.PhysLine }

// Pos returns the 1-based physical line and rune column of the rune at byte
// offset off of Text (or -1,-1 if off is out of range).
func (l *Line) Pos(off int) (int, int) {
	if off < 0 || off >= len(l.phys) {
		return -1, -1
	}
	return l.phys[off], l.col[off]
}

// Scanner yields logical lines with physical position information.
type Scanner struct {
	br      *bufio.Reader
	checked int64
	lineNo  int // physical line number of the line currently read
}

// NewScanner creates a logical-line scanner.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{br: bufio.NewReaderSize(r, 64*1024)}
}

// Checked reports the total number of input bytes examined so far.
func (s *Scanner) Checked() int64 { return s.checked }

func (s *Scanner) readLine() ([]byte, bool) {
	var buf []byte
	for {
		b, err := s.br.ReadByte()
		if err == io.EOF {
			if len(buf) > 0 {
				s.lineNo++
				return buf, true
			}
			return nil, false
		}
		if err != nil {
			return nil, false
		}
		s.checked++
		if b == '\n' {
			s.lineNo++
			return buf, true
		}
		if b == '\r' {
			if c, e := s.br.ReadByte(); e == nil {
				s.checked++
				if c != '\n' {
					s.br.UnreadByte()
				} else {
					s.lineNo++
				}
			} else {
				s.lineNo++
			}
			return buf, true
		}
		buf = append(buf, b)
	}
}

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// Next returns the next logical line. It returns (nil, false) at EOF.
func (s *Scanner) Next() (*Line, bool) {
	var raw []byte
	var col, phys []int
	start := 0
	inComment, cont := false, false

	for {
		pl, ok := s.readLine()
		if !ok {
			if len(raw) > 0 {
				return &Line{string(raw), start, col, phys}, true
			}
			return nil, false
		}
		if !cont && start == 0 && len(raw) == 0 {
			s.lineNo = s.lineNo // lineNo already advanced by readLine
		}
		first := len(raw) == 0
		if first {
			start = s.lineNo
		}

		cur := pl
		i := 0
		if first {
			for i < len(cur) && isWS(cur[i]) {
				i++
			}
			if i == len(cur) {
				continue
			}
			inComment = cur[i] == '#' || cur[i] == '!'
		} else {
			for i < len(cur) && isWS(cur[i]) {
				i++
			}
			if i == len(cur) {
				// Whitespace-only continuation: it contributes nothing and
				// terminates the logical line.
				if len(raw) > 0 {
					return &Line{string(raw), start, col, phys}, true
				}
				continue
			}
		}

		for ; i < len(cur); i++ {
			raw = append(raw, cur[i])
			col = append(col, i+1)
			phys = append(phys, s.lineNo)
		}

		odd := oddTrail(pl)
		if inComment {
			if odd {
				raw = raw[:len(raw)-1]
				col = col[:len(col)-1]
				phys = phys[:len(phys)-1]
			}
			if !odd {
				if len(raw) > 1 {
					return &Line{string(raw), start, col, phys}, true
				}
				raw, col, phys = nil, nil, nil
				inComment, cont = false, false
				continue
			}
			cont = true
			continue
		}

		if odd {
			raw = raw[:len(raw)-1]
			col = col[:len(col)-1]
			phys = phys[:len(phys)-1]
			cont = true
			continue
		}
		return &Line{string(raw), start, col, phys}, true
	}
}

func oddTrail(line []byte) bool {
	n := 0
	for n < len(line) && line[len(line)-1-n] == '\\' {
		n++
	}
	return n%2 == 1
}
