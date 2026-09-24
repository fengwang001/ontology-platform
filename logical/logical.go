// Package logical joins physical lines of a Java .properties stream into
// logical lines: comments, blank lines, backslash continuations and the
// stripping of leading whitespace on continuation lines.
package logical

import (
	"strings"
	"unicode/utf8"
)

// Kind is the classification of a logical line.
type Kind int

const (
	Blank   Kind = iota // empty or whitespace-only
	Comment             // first non-whitespace rune is '#' or '!'
	Data                // anything else
)

// Segment is one physical-line contribution to a logical line.
type Segment struct {
	Line       int    // 1-based physical line number
	StartCol   int    // 1-based column of Text[0] on that line
	Text       string // bytes of this segment within the logical line
	Whole      string // full physical line without its terminator
	ContJoined bool   // true if this line was pulled in as a continuation
}

// Line is a fully assembled logical line.
type Line struct {
	kind Kind
	text string
	segs []Segment
}

// Kind reports the line classification.
func (l Line) Kind() Kind { return l.kind }

// Text is the assembled logical-line text (no terminators).
func (l Line) Text() string { return l.text }

// Segments reports the physical-line pieces, in order.
func (l Line) Segments() []Segment { return l.segs }

// Position maps a logical-text byte offset to its physical 1-based line and
// rune column. It returns the last position for an end-of-text offset.
func (l Line) Position(offset int) (line, col int) {
	idx := len(l.segs) - 1
	for i, s := range l.segs {
		if offset < len(s.Text) {
			idx = i
			break
		}
		offset -= len(s.Text)
	}
	s := l.segs[idx]
	rawOffset := s.StartCol - 1 + offset
	if rawOffset > len(s.Whole) {
		rawOffset = len(s.Whole)
	}
	return s.Line, 1 + utf8.RuneCountInString(s.Whole[:rawOffset])
}

func leadingSkip(line string) int {
	i := 0
	for i < len(line) && isSpace(line[i]) {
		i++
	}
	return i
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// Scanner yields logical lines and counts bytes inspected.
type Scanner struct {
	src        string
	pos        int
	physical   int
	checkCount int
}

// NewScanner starts a scan of src.
func NewScanner(src string) *Scanner { return &Scanner{src: src} }

// Checks reports the total number of source bytes examined so far.
func (s *Scanner) Checks() int { return s.checkCount }

// Next returns the next logical line and false at end of input.
func (s *Scanner) Next() (Line, bool) {
	if s.pos >= len(s.src) {
		return Line{}, false
	}
	var b strings.Builder
	segs := []Segment{}
	first := true
	for {
		start := s.pos
		for s.pos < len(s.src) && s.src[s.pos] != '\n' && s.src[s.pos] != '\r' {
			s.checkCount++
			s.pos++
		}
		raw := s.src[start:s.pos]
		lineNo := s.physical + 1
		s.physical = lineNo
		if s.pos < len(s.src) {
			s.checkCount++
			s.pos++
			if s.src[s.pos-1] == '\r' && s.pos < len(s.src) && s.src[s.pos] == '\n' {
				s.checkCount++
				s.pos++
			}
		}

		end := len(raw)
		trimmed := end
		for trimmed > 0 && isSpace(raw[trimmed-1]) {
			trimmed--
		}
		slashes := 0
		for trimmed-slashes-1 >= 0 && raw[trimmed-slashes-1] == '\\' {
			slashes++
		}
		continues := slashes%2 == 1
		content := raw
		if continues {
			content = raw[:trimmed+slashes-1]
		}

		skip := 0
		if !first {
			skip = leadingSkip(content)
			content = content[skip:]
		}
		b.WriteString(content)
		segs = append(segs, Segment{
			Line: lineNo, StartCol: skip + 1, Text: content, Whole: raw, ContJoined: !first,
		})

		first = false
		if !continues || s.pos >= len(s.src) {
			text := b.String()
			kind := Blank
			if i := leadingSkip(text); i < len(text) && (text[i] == '#' || text[i] == '!') {
				kind = Comment
			} else if i < len(text) {
				kind = Data
			}
			return Line{kind: kind, text: text, segs: segs}, true
		}
	}
}
