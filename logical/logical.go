// Package logical folds physical lines into logical lines following the
// line-assembly rules of java.util.Properties: blank lines, comment lines,
// backslash continuations and stripping of leading whitespace after a
// continuation. It does not interpret keys, values or escapes.
package logical

import "io"

// Kind classifies an assembled logical line.
type Kind int

const (
	Blank   Kind = iota // empty or whitespace-only
	Comment             // first non-whitespace byte is '#' or '!'
	Entry               // anything else
)

// Line is one assembled logical line.
type Line struct {
	Text  string // content with leading whitespace removed and lines joined
	Kind  Kind
	Start int   // 1-based physical line number where the line begins
	segs  []seg // physical-line origin of every byte range in Text
}

type seg struct {
	line int
	end  int // cumulative byte offset in Text at which this segment ends
}

// Pos maps a byte offset inside Text to its 1-based physical line and column.
func (l Line) Pos(off int) (line, col int) {
	prev := 0
	for _, s := range l.segs {
		if off < s.end {
			return s.line, off - prev + 1
		}
		prev = s.end
	}
	return l.Start, off - prev + 1
}

var checked int64

// Checked reports the total number of input bytes inspected since the last
// ResetChecked call.
func Checked() int64 { return checked }

// ResetChecked zeroes the inspection counter.
func ResetChecked() { checked = 0 }

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// Split reads the whole input and returns its logical lines in order.
func Split(r io.Reader) ([]Line, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []Line
	var buf []byte
	var segs []seg
	lineNo, start := 1, 1
	skipping, inEntry, isComment := true, false, false
	runBS := 0
	segLen := 0
	emit := func() {
		if isComment {
			out = append(out, Line{Text: string(buf), Kind: Comment, Start: start, segs: segs})
		} else if !inEntry {
			out = append(out, Line{Text: "", Kind: Blank, Start: start})
		} else {
			out = append(out, Line{Text: string(buf), Kind: Entry, Start: start, segs: segs})
		}
		buf, segs, segLen = buf[:0], nil, 0
		inEntry, isComment, skipping = false, false, true
		runBS = 0
		start = lineNo
	}
	for i := 0; i < len(data); i++ {
		checked++
		c := data[i]
		if c == '\n' || c == '\r' {
			if c == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				checked++
				i++
			}
			if isComment {
				emit()
				lineNo++
				continue
			}
			if inEntry && runBS%2 == 1 {
				buf = buf[:len(buf)-1]
				segLen--
				segs = append(segs, seg{lineNo, len(buf)})
				runBS, skipping = 0, true
				lineNo++
				continue
			}
			emit()
			lineNo++
			continue
		}
		if isComment {
			continue
		}
		if skipping {
			if isWS(c) {
				continue
			}
			skipping = false
			if !inEntry {
				inEntry = true
				start = lineNo
				if c == '#' || c == '!' {
					isComment = true
					continue
				}
			}
		}
		buf = append(buf, c)
		segLen++
		if c == '\\' {
			runBS++
		} else {
			runBS = 0
		}
	}
	if isComment || inEntry {
		if inEntry && runBS%2 == 1 {
			buf = buf[:len(buf)-1]
		}
		emit()
	}
	return out, nil
}
