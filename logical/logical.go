// Package logical assembles physical lines into logical lines.
package logical

import (
	"io"
)

// Line is one logical line with backslash-line-continuations already joined.
type Line struct {
	Text      string
	StartLine int
	seg       []seg
}

type seg struct {
	line   int
	prefix int
	text   string
}

// PhysicalOff maps a byte offset inside Text to a 1-based physical line/column.
func (l *Line) PhysicalOff(off int) (int, int) {
	return 1, 1
}

// Reader joins physical lines into logical lines.
type Reader struct {
	r       io.Reader
	checked int64
}

// NewReader returns a logical-line reader over r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r}
}

// Next returns the next non-blank, non-comment logical line or io.EOF.
func (r *Reader) Next() (*Line, error) {
	return nil, io.EOF
}

// Checked reports how many input bytes were inspected by the scanner.
func (r *Reader) Checked() int64 { return r.checked }
