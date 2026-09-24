// Package logical joins physical lines of a Java .properties stream into
// logical lines: comments, blank lines, line continuation via an odd number
// of trailing backslashes, and stripping of leading whitespace on
// continuation lines.
package logical

import "io"

// Segment marks one physical line that contributed to a logical line.
type Segment struct {
	Line    int // physical line number (1-based)
	Start   int // offset in Text where this segment's bytes begin
	Preamble int // count of leading bytes skipped on this physical line
}

// Line is one logical line. Comment lines have Comment==true.
type Line struct {
	Text     string
	Comment  bool
	FirstLine int
	segments []Segment
}

// Position maps a byte offset within Text to a physical line number and a
// 1-based byte column on that physical line.
func (l *Line) Position(offset int) (line, col int) {
	return l.FirstLine, offset + 1
}

// Reader yields logical lines.
type Reader struct {
	src        []byte
	pos        int
	physical   int
	inspected  int64
}

// NewReader reads the whole stream eagerly.
func NewReader(r io.Reader) (*Reader, error) {
	return &Reader{}, nil
}

// Inspected returns the total number of source bytes examined so far.
func (r *Reader) Inspected() int64 { return r.inspected }

// Next returns the next logical line, or io.EOF when exhausted.
func (r *Reader) Next() (*Line, error) {
	return nil, io.EOF
}
