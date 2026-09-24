// Package logical assembles physical lines of a java.util.Properties stream
// into logical lines (comments, blanks, backslash continuations).
package logical

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Kind classifies a logical line.
type Kind int

const (
	Blank Kind = iota
	Comment
	Data
)

// Fragment is one physical line's contribution to a logical line.
// Line is the 1-based physical line number; Col1 is the 1-based rune
// column of the fragment's first byte in that physical line.
type Fragment struct {
	Text string
	Line int
	Col1 int
}

// Line is a fully assembled logical line.
type Line struct {
	Kind      Kind
	Fragments []Fragment
}

// Scanner reads logical lines from an input stream.
type Scanner struct {
	r        *bufio.Reader
	checked  int64
	physLine int
}

// NewScanner returns a Scanner over r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReader(r), physLine: 1}
}

// Checked reports the total number of input bytes examined so far.
func (s *Scanner) Checked() int64 { return s.checked }

// ErrDone signals the end of input.
var ErrDone = errors.New("logical: no more lines")

// Next returns the next logical line; ErrDone at end of input.
func (s *Scanner) Next() (*Line, error) {
	var (
		frags     []Fragment
		cur       strings.Builder
		inData    bool
		comment   bool
		skipWS    = true
		started   bool
		trailing  int
		fragLine  int
		fragCol   int
		runeCol   int
		flushFrag = func(odd bool) {
			text := cur.String()
			if odd {
				text = text[:len(text)-1]
			}
			if text != "" {
				frags = append(frags, Fragment{Text: text, Line: fragLine, Col1: fragCol})
			}
			cur.Reset()
			trailing = 0
		}
	)
	for {
		b, err := s.r.ReadByte()
		if err == nil {
			s.checked++
			started = true
		} else {
			if !started {
				return nil, ErrDone
			}
			switch {
			case inData:
				flushFrag(trailing%2 == 1)
				return &Line{Kind: Data, Fragments: frags}, nil
			case comment:
				flushFrag(false)
				return &Line{Kind: Comment, Fragments: frags}, nil
			default:
				return &Line{Kind: Blank}, nil
			}
		}
		if b == '\r' {
			if nb, e := s.r.ReadByte(); e == nil {
				s.checked++
				if nb != '\n' {
					_ = s.r.UnreadByte()
				}
			}
			b = '\n'
		}
		if b == '\n' {
			switch {
			case comment:
				flushFrag(false)
				return &Line{Kind: Comment, Fragments: frags}, nil
			case !inData:
				return &Line{Kind: Blank}, nil
			case trailing%2 == 1:
				flushFrag(true)
				s.physLine++
				runeCol = 0
				skipWS = true
			default:
				flushFrag(false)
				return &Line{Kind: Data, Fragments: frags}, nil
			}
			continue
		}
		if b < 0x80 || b >= 0xC0 {
			runeCol++
		}
		if comment {
			cur.WriteByte(b)
			continue
		}
		if skipWS && isWS(b) {
			continue
		}
		if skipWS {
			if !inData && (b == '#' || b == '!') {
				comment = true
				fragLine = s.physLine
				fragCol = runeCol
				cur.WriteByte(b)
				continue
			}
			skipWS = false
			inData = true
			fragLine = s.physLine
			fragCol = runeCol
		}
		cur.WriteByte(b)
		if b == '\\' {
			trailing++
		} else {
			trailing = 0
		}
	}
}

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }
