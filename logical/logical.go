// Package logical assembles physical lines of a Java .properties stream
// into logical lines: blank lines and comment lines are dropped, lines
// ending in an odd number of backslashes are joined with the next
// physical line after stripping that line's leading whitespace.
package logical

import (
	"bufio"
	"io"
	"unicode/utf8"
)

// seg maps a byte range of Line.Text back to its physical position.
type seg struct {
	line, col  int // 1-based physical position of the first byte
	start, end int // byte offsets into Line.Text
}

// Line is one logical line assembled from one or more physical lines.
type Line struct {
	Text string
	segs []seg
}

// Position maps a byte offset in Text to a 1-based physical line and
// column (columns count runes).
func (l *Line) Position(off int) (line, col int) {
	for _, s := range l.segs {
		if off < s.end {
			return s.line, s.col + utf8.RuneCountInString(l.Text[s.start:off])
		}
	}
	last := l.segs[len(l.segs)-1]
	return last.line, last.col + utf8.RuneCountInString(l.Text[last.start:last.end])
}

// Reader assembles logical lines from an underlying byte stream.
type Reader struct {
	br   *bufio.Reader
	n    int64 // total number of input bytes inspected
	line int   // current physical line number
	col  int   // current rune column within the physical line
}

// NewReader returns a Reader reading from r.
func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r), line: 1}
}

// Inspected reports how many input bytes have been examined so far.
func (r *Reader) Inspected() int64 { return r.n }

// next returns the next byte, normalizing "\r" and "\r\n" to '\n'.
func (r *Reader) next() (byte, error) {
	b, err := r.br.ReadByte()
	if err != nil {
		return 0, err
	}
	r.n++
	switch {
	case b == '\r':
		r.line, r.col = r.line+1, 0
		if p, _ := r.br.Peek(1); len(p) == 1 && p[0] == '\n' {
			r.br.ReadByte()
			r.n++
		}
		return '\n', nil
	case b == '\n':
		r.line, r.col = r.line+1, 0
	case b&0xC0 != 0x80: // count runes, not bytes
		r.col++
	}
	return b, nil
}

// Read returns the next content logical line, skipping blank and comment
// lines. It returns io.EOF when the stream is exhausted.
func (r *Reader) Read() (*Line, error) {
	var buf []byte
	var segs []seg
	skipWS, appended, odd, comment, first := true, false, false, false, true
	open := -1
	for {
		b, err := r.next()
		if err == io.EOF {
			if len(buf) == 0 {
				return nil, io.EOF
			}
			if odd { // dangling backslash at EOF is dropped
				buf = buf[:len(buf)-1]
			}
			break
		}
		if err != nil {
			return nil, err
		}
		if skipWS {
			if b == ' ' || b == '\t' || b == '\f' {
				continue
			}
			if !appended && b == '\n' {
				continue // blank physical line before any content
			}
			skipWS, appended = false, false
			if first && (b == '#' || b == '!') {
				comment = true
			}
			first = false
			if b != '\n' {
				segs = append(segs, seg{line: r.line, col: r.col, start: len(buf)})
				open = len(segs) - 1
			}
		}
		if b == '\n' {
			if comment || len(buf) == 0 {
				buf, segs, open = buf[:0], nil, -1
				skipWS, appended, odd, comment, first = true, false, false, false, true
				continue
			}
			if odd { // continuation: drop the backslash, join next line
				buf = buf[:len(buf)-1]
				segs[open].end = len(buf)
				skipWS, appended, odd = true, true, false
				continue
			}
			break
		}
		if !comment {
			buf = append(buf, b)
			if b == '\\' {
				odd = !odd
			} else {
				odd = false
			}
		}
	}
	if open >= 0 {
		segs[open].end = len(buf)
	}
	if len(buf) == 0 { // lone dangling backslash at EOF
		return nil, io.EOF
	}
	return &Line{Text: string(buf), segs: segs}, nil
}
