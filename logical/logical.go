// Package logical joins physical lines of a .properties stream into logical
// lines: comments, blank lines, backslash line continuation and leading
// whitespace stripping after a continuation.
package logical

// Line is one logical line.
type Line struct {
	Text string
	segs []seg
}

// Position converts a byte offset in Text into a 1-based physical line number
// and byte column.
func (l Line) Position(off int) (line, col int) {
	return 1, 1
}

// Scanner yields logical lines from the input bytes.
type Scanner struct {
	src     []byte
	checked int64
}

// NewScanner returns a Scanner over src.
func NewScanner(src []byte) *Scanner {
	return &Scanner{src: src}
}

// Next returns the next logical line; ok is false at end of input.
func (s *Scanner) Next() (l Line, ok bool) {
	return Line{}, false
}

// Checked reports the total number of bytes examined while deciding
// continuation parity.
func (s *Scanner) Checked() int64 {
	return s.checked
}
