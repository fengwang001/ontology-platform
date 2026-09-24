// Package logical joins physical lines of a Java .properties file into
// logical lines: comment and blank lines are dropped, and continuations
// (an odd number of trailing backslashes) are joined after stripping the
// leading whitespace of every physical line.
package logical

import "io"

// Line is one logical line.
type Line struct {
	Text string // content, leading whitespace stripped
	Num  int    // 1-based physical line number where the logical line starts
	Col  int    // 1-based column on that line where Text starts
}

// checked counts bytes examined by Split. Every byte is looked at O(1)
// times: once by the newline scan, plus at most once each by the leading
// whitespace strip and the trailing backslash parity scan of its line.
var checked int64

// Checked reports how many bytes Split has examined since the last Reset.
func Checked() int64 { return checked }

// Reset zeroes the examination counter.
func Reset() { checked = 0 }

func isWS(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

// Split reads all of r and returns its logical lines in order.
func Split(r io.Reader) ([]Line, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var out []Line
	var buf []byte
	num, col := 0, 0
	open, cont := false, false
	for pos, phys := 0, 1; pos < len(data); phys++ {
		end := pos
		for end < len(data) {
			checked++
			if data[end] == '\n' {
				break
			}
			end++
		}
		line := data[pos:end]
		pos = end + 1
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		k := 0
		for k < len(line) {
			checked++
			if !isWS(line[k]) {
				break
			}
			k++
		}
		body := line[k:]
		if !cont {
			if len(body) == 0 || body[0] == '#' || body[0] == '!' {
				continue
			}
			open, num, col = true, phys, k+1
		}
		bs := 0
		for p := len(body) - 1; p >= 0; p-- {
			checked++
			if body[p] != '\\' {
				break
			}
			bs++
		}
		if cont = bs%2 == 1; cont {
			body = body[:len(body)-1]
		}
		buf = append(buf, body...)
		if !cont {
			out = append(out, Line{Text: string(buf), Num: num, Col: col})
			buf = buf[:0]
			open = false
		}
	}
	if open {
		out = append(out, Line{Text: string(buf), Num: num, Col: col})
	}
	return out, nil
}
