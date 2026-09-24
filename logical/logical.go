// Package logical assembles the physical lines of a Java
// .properties file into logical lines: comment and blank lines are
// dropped, a trailing odd run of backslashes joins the next physical
// line whose leading whitespace is stripped.
package logical

import (
	"bufio"
	"io"
	"strings"
)

// checked counts how many input bytes the scans below inspected
// since the last Lines call. It exists to prove linearity.
var checked int64

// Checked returns the number of byte inspections so far.
func Checked() int64 { return checked }

// Seg maps one chunk of a logical line back to its physical line.
type Seg struct {
	Line   int // 1-based physical line number
	Offset int // byte offset of the chunk inside Line.Text
}

// Line is one logical line plus its physical origin.
type Line struct {
	Text  string
	Start int // 1-based physical line of the first chunk
	Segs  []Seg
}

// Position maps a byte offset inside Text to a 1-based physical
// line and column.
func (l Line) Position(off int) (line, col int) {
	line, col = l.Start, off+1
	for _, s := range l.Segs {
		if s.Offset <= off {
			line, col = s.Line, off-s.Offset+1
		}
	}
	return line, col
}

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// stripLeft removes leading whitespace, inspecting each byte once.
func stripLeft(s string) string {
	i := 0
	for i < len(s) && isWS(s[i]) {
		i++
	}
	checked += int64(i)
	if i < len(s) {
		checked++
	}
	return s[i:]
}

// oddTailSlash reports whether s ends in an odd number of
// backslashes, scanning backwards from the tail only.
func oddTailSlash(s string) bool {
	n := 0
	for n < len(s) && s[len(s)-1-n] == '\\' {
		n++
	}
	checked += int64(n)
	if n < len(s) {
		checked++
	}
	return n%2 == 1
}

// Lines reads r and returns its logical lines in order.
func Lines(r io.Reader) ([]Line, error) {
	checked = 0
	var out []Line
	var text strings.Builder
	var segs []Seg
	start, phys := 0, 0
	cont := false
	in := bufio.NewReader(r)
	flush := func() {
		if text.Len() > 0 {
			out = append(out, Line{Text: text.String(), Start: start, Segs: segs})
			text.Reset()
			segs = nil
		}
	}
	for {
		raw, err := in.ReadString('\n')
		if len(raw) > 0 {
			phys++
			body := stripLeft(strings.TrimRight(strings.TrimRight(raw, "\n"), "\r"))
			switch {
			case !cont && (body == "" || body[0] == '#' || body[0] == '!'):
				// Blank or comment physical line: skipped.
			default:
				if !cont {
					start = phys
				}
				segs = append(segs, Seg{Line: phys, Offset: text.Len()})
				text.WriteString(body)
				if cont = oddTailSlash(body); cont {
					s := text.String()
					text.Reset()
					text.WriteString(s[:len(s)-1])
				} else {
					flush()
				}
			}
		}
		if err != nil {
			flush()
			if err == io.EOF {
				return out, nil
			}
			return out, err
		}
	}
}
