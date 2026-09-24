// Package logical joins physical lines of a Java .properties stream into
// logical lines: comments, blank lines, backslash continuation, and
// stripping of leading whitespace on continued physical lines.
package logical

import (
	"bufio"
	"io"
)

// Kind identifies the kind of a logical line.
type Kind int

const (
	// Data is a key/value line; Comment begins with '#' or '!'.
	Data Kind = iota
	Comment
)

// Segment is the text contributed by one physical line after any leading
// whitespace stripping. Line is its 1-based physical line number; Col is
// the 1-based rune column at which Text began on that physical line.
type Segment struct {
	Line int
	Col  int
	Text string
}

// Line is a logical line made of one or more physical-line segments.
type Line struct {
	Kind     Kind
	Line     int // physical line where the logical line began
	Segments []Segment
}

// Scanner reads logical lines from a stream.
type Scanner struct {
	r         *bufio.Reader
	physical  int
	examined  int64
	skipLF    bool
}

// NewScanner returns a Scanner over r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReader(r)}
}

// Examined reports the total number of input bytes consumed so far.
func (s *Scanner) Examined() int64 { return s.examined }

func (s *Scanner) read() (byte, error) {
	b, err := s.r.ReadByte()
	if err == nil {
		s.examined++
	}
	return b, err
}

// Next returns the next logical line. It returns io.EOF when the stream is
// exhausted; purely blank physical lines are skipped silently.
func (s *Scanner) Next() (*Line, error) {
	var (
		segs        []Segment
		buf         []byte
		segLine     int
		col, segCol int
		first       = true
		strip       = false
		kind        = Data
	)
	emit := func() {
		if len(buf) > 0 {
			segs = append(segs, Segment{Line: segLine, Col: segCol, Text: string(buf)})
			buf = nil
		}
	}
scan:
	for {
		b, err := s.read()
		if err == io.EOF {
			emit()
			if len(segs) == 0 {
				return nil, io.EOF
			}
			break scan
		}
		if err != nil {
			return nil, err
		}
		if s.skipLF {
			s.skipLF = false
			if b == '\n' {
				continue
			}
		}
		col++
		switch {
		case b == '\n' || b == '\r':
			if b == '\r' {
				s.skipLF = true
			}
			s.physical++
			emit()
			if len(segs) == 0 {
				first, strip, kind = true, false, Data
				col = 0
				continue
			}
			break scan
		case b == '\\':
			n, nerr := s.read()
			if nerr == io.EOF {
				emit()
				if len(segs) == 0 {
					return nil, io.EOF
				}
				break scan
			}
			if nerr != nil {
				return nil, nerr
			}
			if n == '\n' || n == '\r' {
				if n == '\r' {
					s.skipLF = true
				}
				s.physical++
				emit()
				strip = true
				col = 0
				continue
			}
			col++
			buf = append(buf, b, n)
		case first && (b == '#' || b == '!'):
			kind = Comment
			first = false
		case (first || strip) && (b == ' ' || b == '\t' || b == '\f'):
			// leading whitespace of first / continued physical line
		default:
			if first || strip {
				first, strip = false, false
				segLine = s.physical + 1
				segCol = col
			}
			buf = append(buf, b)
		}
	}
	return &Line{Kind: kind, Line: segs[0].Line, Segments: segs}, nil
}
