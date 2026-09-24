// Package logical folds physical lines of a Java .properties stream into
// logical lines (comment filtering, line-continuation, leading-space strip).
package logical

import (
	"bufio"
	"io"
)

// Segment is one physical line's contribution to a logical line.
type Segment struct {
	Text   string // content after stripping leading whitespace
	Line   int    // physical line number (1-based)
	Indent int    // stripped leading whitespace bytes (column offset)
}

// Line is a folded logical line. Comment lines and blank lines are not
// returned: Next yields only logical lines carrying data.
type Line struct {
	Segments []Segment
}

// Reader reads logical lines.
type Reader struct {
	br     *bufio.Reader
	lineNo int // physical line of the last byte consumed
	pend   byte
	hasPend bool
	checks int64
}

// NewReader returns a logical-line Reader over r.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r)}
}

// Next returns the next data-bearing logical line, or io.EOF.
func (r *Reader) Next() (*Line, error) {
	for {
		line, comment, err := r.readLogical()
		if err != nil {
			return nil, err
		}
		if comment {
			continue
		}
		return line, nil
	}
}

// Checks reports the number of bytes inspected while folding lines.
func (r *Reader) Checks() int64 { return r.checks }

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// readByte returns exactly one input byte; every input byte is counted once.
func (r *Reader) readByte() (byte, error) {
	if r.hasPend {
		r.hasPend = false
		return r.pend, nil
	}
	c, err := r.br.ReadByte()
	if err == nil {
		r.checks++
	}
	return c, err
}

func (r *Reader) unread(c byte) { r.pend, r.hasPend = c, true }

// readLogical folds one logical line; comment reports a comment line.
func (r *Reader) readLogical() (line *Line, comment bool, err error) {
	var segs []Segment
	inComment := false
	for {
		r.lineNo++
		physLine, indent := r.lineNo, 0

		// Strip leading whitespace of this physical line.
		var c byte
		for {
			c, err = r.readByte()
			if err == io.EOF {
				if len(segs) > 0 {
					return &Line{Segments: segs}, false, nil
				}
				return nil, false, io.EOF
			}
			if err != nil {
				return nil, false, err
			}
			if isSpace(c) {
				indent++
				continue
			}
			break
		}

		// Blank physical line: terminates a continuation, otherwise skipped.
		if c == '\n' || c == '\r' {
			r.consumeLineEnd(c)
			if len(segs) > 0 {
				return &Line{Segments: segs}, false, nil
			}
			continue
		}

		if len(segs) == 0 && !inComment && (c == '#' || c == '!') {
			inComment = true
		}
		isComment := inComment
		var text []byte
		oddBack := false
		for {
			if !isComment {
				text = append(text, c)
			}
			if c == '\\' {
				oddBack = !oddBack
			} else {
				oddBack = false
			}
			c, err = r.readByte()
			if err == io.EOF {
				if oddBack && len(text) > 0 {
					text = text[:len(text)-1] // dangling backslash dropped
				}
				if isComment {
					return nil, true, nil
				}
				segs = append(segs, Segment{Text: string(text), Line: physLine, Indent: indent})
				return &Line{Segments: segs}, false, nil
			}
			if err != nil {
				return nil, false, err
			}
			if c != '\n' && c != '\r' {
				continue
			}
			r.consumeLineEnd(c)
			if isComment && !oddBack {
				return nil, true, nil
			}
			if oddBack {
				if len(text) > 0 {
					text = text[:len(text)-1]
				}
				if !isComment {
					segs = append(segs, Segment{Text: string(text), Line: physLine, Indent: indent})
				}
				break // next physical line
			}
			segs = append(segs, Segment{Text: string(text), Line: physLine, Indent: indent})
			return &Line{Segments: segs}, false, nil
		}
	}
}

// consumeLineEnd absorbs a newline (\r, \n, or \r\n) as one physical line end.
func (r *Reader) consumeLineEnd(first byte) {
	if first != '\r' {
		return
	}
	c, err := r.readByte()
	if err != nil {
		return
	}
	if c != '\n' {
		r.unread(c) // lone \r: byte belongs to the next physical line
	}
}
