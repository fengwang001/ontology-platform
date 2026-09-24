// Package logical joins physical lines of a Java .properties stream into
// logical lines: it skips blank/comment lines, honors backslash-newline
// continuations and strips leading whitespace of continuation segments.
package logical

import (
	"bufio"
	"io"
)

// Cell is one decoded rune of a logical line with its physical position.
type Cell struct {
	Rune rune
	Line int // 1-based physical line number
	Col  int // 1-based rune column within that physical line
}

// Line is one logical line with per-cell physical positions.
type Line struct {
	Cells []Cell
}

// Reader produces logical lines.
type Reader struct {
	r        *bufio.Reader
	pending  Cell // one-cell pushback (for "\r" not followed by "\n")
	hasPend  bool
	snapL    int // position snapshot restored on pushback
	snapC    int
	line     int // physical line of the next rune to return
	col      int // physical column of the next rune to return
	examined int64
}

// NewReader creates a Reader over r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, 64*1024), line: 1, col: 1}
}

// readCell decodes one rune, counts its bytes once, and reports position.
func (r *Reader) readCell() (Cell, error) {
	if r.hasPend {
		r.hasPend = false
		r.line, r.col = r.snapL, r.snapC
		return r.pending, nil
	}
	r.snapL, r.snapC = r.line, r.col
	ch, size, err := r.r.ReadRune()
	if err != nil {
		return Cell{}, err
	}
	r.examined += int64(size)
	cell := Cell{Rune: ch, Line: r.line, Col: r.col}
	if ch == '\n' || ch == '\r' {
		r.line++
		r.col = 1
	} else {
		r.col++
	}
	return cell, nil
}

func (r *Reader) unread(c Cell) { r.pending = c; r.hasPend = true }

// endLine consumes a terminator starting at c, collapsing "\r\n" to one.
func (r *Reader) endLine(c Cell) {
	if c.Rune != '\r' {
		return
	}
	if next, err := r.readCell(); err == nil && next.Rune != '\n' {
		r.unread(next)
	}
}

func isSpace(ch rune) bool { return ch == ' ' || ch == '\t' || ch == '\f' }

// Next returns the next key/value logical line, skipping blank and comment
// lines. It returns io.EOF when the input is exhausted.
func (r *Reader) Next() (*Line, error) {
	for {
		cells := make([]Cell, 0, 64)
		skipSpace, comment, escaped := true, false, false
		c, err := r.readCell()
		for err == nil {
			if c.Rune == '\n' || c.Rune == '\r' {
				r.endLine(c)
				if !escaped {
					break
				}
				skipSpace, escaped = true, false
				c, err = r.readCell()
				continue
			}
			if skipSpace && isSpace(c.Rune) {
				c, err = r.readCell()
				continue
			}
			skipSpace = false
			if !comment && (c.Rune == '#' || c.Rune == '!') {
				comment, cells = true, cells[:0]
				c, err = r.readCell()
				continue
			}
			if comment {
				c, err = r.readCell()
				continue
			}
			if c.Rune == '\\' {
				next, nerr := r.readCell()
				if nerr != nil { // lone trailing backslash at EOF: dropped
					break
				}
				if next.Rune == '\n' || next.Rune == '\r' {
					r.endLine(next)
					escaped, skipSpace = true, true
					c, err = r.readCell()
					continue
				}
				cells = append(cells, c, next)
				escaped = next.Rune == '\\'
				c, err = r.readCell()
				continue
			}
			escaped = false
			cells = append(cells, c)
			c, err = r.readCell()
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		if len(cells) > 0 {
			return &Line{Cells: cells}, nil
		}
		if err == io.EOF {
			return nil, io.EOF
		}
	}
}

// ExaminedBytes reports how many input bytes have been inspected so far.
func (r *Reader) ExaminedBytes() int64 { return r.examined }
