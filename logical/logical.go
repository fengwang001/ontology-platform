// Package logical assembles physical lines of a Java .properties stream into
// logical lines: comments, blank lines, and backslash continuations.
package logical

import "unicode/utf8"

// Kind classifies a logical line.
type Kind int

const (
	Blank   Kind = iota // empty or whitespace-only physical line
	Comment             // first non-whitespace byte is '#' or '!'
	Normal              // key/value content, with continuation applied
)

// Line is one logical line.
type Line struct {
	Kind Kind
	Text string // Normal: assembled content; Comment: text incl. marker; Blank: ""
	Line int    // 1-based physical line number where this logical line starts
	seg  []segment
}

// segment maps one physical-line contribution into the assembled text.
type segment struct {
	physLine int // 1-based physical line number
	start    int // 0-based rune column of the first kept rune
	lo, hi   int // logical byte offsets [lo, hi) covered
}

// Scanner splits properties input into logical lines.
type Scanner struct {
	src    []byte
	pos    int
	lineNo int // physical line number of the byte at pos (1-based)
	checks int // unexported: source bytes examined
}

// NewScanner returns a scanner over src.
func NewScanner(src []byte) *Scanner { return &Scanner{src: src, lineNo: 1} }

// Next returns the next logical line, or nil at end of input.
func (s *Scanner) Next() *Line {
	if s.pos >= len(s.src) {
		return nil
	}
	var buf []byte
	var segs []segment
	head, first := 0, true
	for {
		line, startCol, phys, trail, more := s.readPhysical()
		switch {
		case first && len(line) == 0:
			return &Line{Kind: Blank, Line: phys}
		case first && (line[0] == '#' || line[0] == '!'):
			return &Line{Kind: Comment, Text: string(line), Line: phys}
		case !first && len(line) == 0:
			// An empty/whitespace-only continuation line terminates the line.
			return &Line{Kind: Normal, Text: string(buf), Line: head, seg: segs}
		}
		keep := line
		if trail%2 == 1 {
			keep = line[:len(line)-1] // drop exactly the joining backslash
		}
		if first {
			head = phys
		}
		segs = append(segs, segment{phys, startCol, len(buf), len(buf) + len(keep)})
		buf = append(buf, keep...)
		first = false
		if trail%2 != 1 || !more {
			return &Line{Kind: Normal, Text: string(buf), Line: head, seg: segs}
		}
	}
}

// readPhysical reads one physical line (without terminator), strips leading
// whitespace, and counts the trailing run of backslashes. Each byte is
// examined once. more reports whether another physical line follows.
func (s *Scanner) readPhysical() (line []byte, startCol, phys, trail int, more bool) {
	phys = s.lineNo
	startByte := -1
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		s.checks++
		s.pos++
		if c == '\n' || c == '\r' {
			if c == '\r' && s.pos < len(s.src) && s.src[s.pos] == '\n' {
				s.pos++
			}
			s.lineNo++
			return line, s.runeCol(startByte), phys, trail, s.pos < len(s.src)
		}
		if startByte < 0 && (c == ' ' || c == '\t' || c == '\f') {
			continue // strip leading whitespace
		}
		if startByte < 0 {
			startByte = s.pos - 1
		}
		line = append(line, c)
		if c == '\\' {
			trail++
		} else {
			trail = 0
		}
	}
	return line, s.runeCol(startByte), phys, trail, false
}

// runeCol turns a byte index on the current physical line (or -1) into a
// 0-based rune column.
func (s *Scanner) runeCol(byteCol int) int {
	if byteCol < 0 {
		return 0
	}
	start := byteCol
	for start > 0 && s.src[start-1] != '\n' && s.src[start-1] != '\r' {
		start--
	}
	return utf8.RuneCount(s.src[start:byteCol])
}

// Checks returns the total number of source bytes examined so far.
func (s *Scanner) Checks() int { return s.checks }

// Position converts a 0-based logical-text offset into its 1-based physical
// line and rune column; past-end offsets clamp to the final position.
func (l *Line) Position(off int) (line, col int) {
	if len(l.seg) == 0 {
		return l.Line, 1
	}
	sg := l.seg[len(l.seg)-1]
	for _, x := range l.seg {
		if off < x.hi {
			sg = x
			break
		}
	}
	if off > sg.hi {
		off = sg.hi
	}
	return sg.physLine, sg.start + utf8.RuneCountInString(l.Text[sg.lo:off]) + 1
}
