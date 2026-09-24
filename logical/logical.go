// Package logical assembles physical lines into logical lines following
// the rules of java.util.Properties.load(Reader).
package logical

import (
	"bufio"
	"io"
)

// Kind classifies a logical line.
type Kind int

const (
	// Blank is a line containing only whitespace (or empty).
	Blank Kind = iota
	// Comment starts with '#' or '!' after leading whitespace.
	Comment
	// Data is a key/value-bearing logical line.
	Data
)

// Segment is one physical line fragment of a logical line.
// The first segment is the start physical line; later segments are
// continuation lines with leading whitespace already stripped.
type Segment struct {
	Text  string
	Line  int // 1-based physical line number
	Lead  int // count of leading whitespace runes stripped from the raw line
}

// Line is one logical line assembled from one or more physical lines.
type Line struct {
	Kind     Kind
	Segments []Segment
}

// Scanner splits a reader into logical lines.
type Scanner struct {
	r       *bufio.Reader
	phys    int   // 1-based physical line number of the next line
	checks  int64 // total input bytes inspected
}

// NewScanner creates a Scanner over r.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{r: bufio.NewReader(r)}
}

// Next returns the next logical line, or nil at EOF.
func (s *Scanner) Next() *Line {
	raw, line, err := s.readLine()
	if err != nil {
		return nil
	}
	body := dropTerminator(raw)
	lead, content := trimLead(body)
	switch {
	case len(content) == 0:
		return &Line{Kind: Blank, Segments: []Segment{{body, line, lead}}}
	case content[0] == '#' || content[0] == '!':
		return &Line{Kind: Comment, Segments: []Segment{{body, line, lead}}}
	}
	segs := []Segment{{body, line, lead}}
	for endsOddBackslash(content) {
		// Drop exactly one trailing backslash (the line-continuation one).
		content = content[:len(content)-1]
		segs[len(segs)-1].Text = content
		raw, line, err = s.readLine()
		if err != nil {
			return &Line{Kind: Data, Segments: segs}
		}
		body = dropTerminator(raw)
		lead, content = trimLead(body)
		segs = append(segs, Segment{content, line, lead})
	}
	return &Line{Kind: Data, Segments: segs}
}

// Checks reports the number of input bytes inspected so far.
func (s *Scanner) Checks() int64 {
	return s.checks
}

func (s *Scanner) readLine() (string, int, error) {
	raw, err := s.r.ReadBytes('\n')
	s.checks += int64(len(raw))
	if len(raw) == 0 {
		return "", 0, err
	}
	s.phys++
	return string(raw), s.phys, err
}

func dropTerminator(line string) string {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\f'
}

// trimLead strips leading Properties whitespace and reports how many
// whitespace runes were removed.
func trimLead(line string) (int, string) {
	i, n := 0, 0
	for i < len(line) && isSpaceByte(line[i]) {
		i++
		n++
	}
	return n, line[i:]
}

// endsOddBackslash reports whether content ends in an odd number of
// backslashes. Trailing whitespace ends the run: the JDK checks parity
// only over the backslashes immediately before the line terminator, and
// an odd run escapes that terminator. Whitespace after backslashes means
// the terminator itself is not escaped.
func endsOddBackslash(content string) bool {
	n := 0
	for n < len(content) && content[len(content)-1-n] == '\\' {
		n++
	}
	return n%2 == 1
}
