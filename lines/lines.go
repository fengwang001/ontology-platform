// Package lines splits byte strings into logical lines while preserving each
// original line terminator (\n, \r\n, or no terminator on the final line),
// so the exact input can be reconstructed byte-for-byte.
package lines

// Line is one logical line. Text excludes the terminator; End records it:
// "\n", "\r\n", or "" when this is the final line without a newline.
type Line struct {
	Text string // line content without terminator
	End  string // original terminator: "\n", "\r\n", or ""
}

// NL reports whether the line is terminated by a newline.
func (l Line) NL() bool { return l.End != "" }

// Split slices data into Lines preserving terminators.
// The empty input yields zero lines (not one empty unterminated line).
func Split(data string) []Line {
	ls := []Line{}
	i := 0
	for i < len(data) {
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		text := data[i:j]
		end := ""
		if j < len(data) { // found '\n' at j
			end = "\n"
			if len(text) > 0 && text[len(text)-1] == '\r' {
				text = text[:len(text)-1]
				end = "\r\n"
			}
			j++
		}
		ls = append(ls, Line{Text: text, End: end})
		i = j
	}
	return ls
}

// Join reconstructs the exact original byte string from lines.
func Join(ls []Line) string {
	b := make([]byte, 0)
	for _, l := range ls {
		if l.End == "\r\n" {
			b = append(b, l.Text...)
			b = append(b, '\r', '\n')
		} else {
			b = append(b, l.Text...)
			b = append(b, l.End...)
		}
	}
	return string(b)
}

// Equal compares two line sequences for exact equality.
func Equal(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
