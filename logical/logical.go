// Package logical joins physical lines of a Java .properties file into
// logical lines: it drops comment and blank lines, joins continuation
// lines (odd trailing backslashes) and strips leading whitespace of
// continuation lines.
package logical

var checked int64

// Checked reports how many input bytes have been inspected so far.
func Checked() int64 { return checked }

// ResetChecked zeroes the inspection counter.
func ResetChecked() { checked = 0 }

// Line is a logical line.
type Line struct {
	Text string // content, leading whitespace stripped
	Num  int    // 1-based number of its first physical line
}

func isWS(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// eol consumes one line terminator (\n, \r\n or \r) at i and
// returns the new offset.
func eol(data []byte, i int) int {
	c := data[i]
	checked++
	i++
	if c == '\r' && i < len(data) && data[i] == '\n' {
		checked++
		i++
	}
	return i
}

// Lines splits data into logical lines. Comment lines (first
// non-whitespace byte '#' or '!') and blank lines are dropped. An odd
// run of trailing backslashes continues the line: one backslash is
// removed and the next physical line, its leading whitespace stripped,
// is appended. A whitespace-only continuation line therefore appends
// nothing and ends the chain. Trailing backslashes are counted from
// the end of each segment, never rescanning from the start.
func Lines(data []byte) []Line {
	var out []Line
	n := len(data)
	i := 0
	num := 1
	for i < n {
		for i < n && isWS(data[i]) {
			checked++
			i++
		}
		if i >= n {
			break
		}
		c := data[i]
		checked++
		switch {
		case c == '\n' || c == '\r':
			i = eol(data, i)
			num++
			continue
		case c == '#' || c == '!':
			for i < n && data[i] != '\n' && data[i] != '\r' {
				checked++
				i++
			}
			if i < n {
				i = eol(data, i)
				num++
			}
			continue
		}
		start := num
		var buf []byte
		for {
			segStart := i
			for i < n && data[i] != '\n' && data[i] != '\r' {
				checked++
				i++
			}
			seg := data[segStart:i]
			eof := i >= n
			if !eof {
				i = eol(data, i)
				num++
			}
			bs := 0
			for j := len(seg) - 1; j >= 0 && seg[j] == '\\'; j-- {
				checked++
				bs++
			}
			if bs%2 == 1 {
				seg = seg[:len(seg)-1]
			}
			buf = append(buf, seg...)
			if bs%2 == 0 || eof {
				break
			}
			for i < n && isWS(data[i]) {
				checked++
				i++
			}
		}
		if len(buf) > 0 {
			out = append(out, Line{Text: string(buf), Num: start})
		}
	}
	return out
}
