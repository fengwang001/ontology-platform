// Package logical joins physical lines of a Java .properties file into
// logical lines: it drops comment and blank lines, joins continuation
// lines (odd trailing backslashes), and strips leading whitespace.
package logical

import "io"

// Seg maps one joined segment of a logical line back to its physical line.
type Seg struct {
	Line int // 1-based physical line number
	Off  int // byte offset of the segment within Line.Text
	Col  int // 1-based column of the segment's first byte in its physical line
}

// Line is a logical line: one or more physical lines joined by continuation.
type Line struct {
	Text string
	Segs []Seg
}

var inspected int64 // bytes examined since the last Reset

// Inspected reports how many input bytes have been examined since Reset.
func Inspected() int64 { return inspected }

// Reset zeroes the inspection counter.
func Reset() { inspected = 0 }

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// Read splits src into logical lines, dropping comments and blank lines.
func Read(src io.Reader) ([]Line, error) {
	buf, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	var out []Line
	var text []byte
	var segs []Seg
	open := false
	phys, pos := 0, 0
	for pos < len(buf) {
		start := pos
		for pos < len(buf) && buf[pos] != '\n' {
			pos++
		}
		inspected += int64(pos - start)
		phys++
		line := buf[start:pos]
		if pos < len(buf) {
			pos++
			inspected++
		}
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		inspected++
		i := 0
		for i < len(line) && isWS(line[i]) {
			i++
		}
		inspected += int64(i)
		if i < len(line) {
			inspected++
		}
		c := line[i:]
		if !open {
			if len(c) == 0 || c[0] == '#' || c[0] == '!' {
				continue // blank or comment line; comments never continue
			}
			text, segs = text[:0], segs[:0]
			open = true
		}
		b := 0
		for b < len(c) && c[len(c)-1-b] == '\\' {
			b++
		}
		inspected += int64(b)
		if b < len(c) {
			inspected++
		}
		cont := b%2 == 1
		if cont {
			c = c[:len(c)-1]
		}
		segs = append(segs, Seg{Line: phys, Off: len(text), Col: i + 1})
		text = append(text, c...)
		if !cont {
			out = append(out, Line{string(text), append([]Seg(nil), segs...)})
			open = false
		}
	}
	if open { // file ends in a continuation: the trailing backslash is dropped
		out = append(out, Line{string(text), segs})
	}
	return out, nil
}
