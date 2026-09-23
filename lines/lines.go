// Package lines splits byte strings into logical lines while preserving each
// line's original terminator (\n, \r\n, or none for the final line).
package lines

// Line is one logical line. Text excludes the terminator; End is the raw
// terminator ("\n", "\r\n") or "" when the line is the final, unterminated one.
type Line struct {
	Text string
	End  string
}

// HasNL reports whether the line carried a terminator.
func (l Line) HasNL() bool { return l.End != "" }

// Raw returns the line exactly as it appeared in the input.
func (l Line) Raw() string { return l.Text + l.End }

// Split cuts data into lines. An empty input yields zero lines (distinct from
// one empty unterminated line, which only occurs for a non-empty input).
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i + 1
		term := "\n"
		textEnd := i
		if i > start && data[i-1] == '\r' {
			textEnd = i - 1
			term = "\r\n"
		}
		out = append(out, Line{Text: string(data[start:textEnd]), End: term})
		start = end
		i = start - 1
	}
	if start < len(data) {
		out = append(out, Line{Text: string(data[start:]), End: ""})
	}
	return out
}

// Join reconstructs the exact bytes the lines were split from.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.End)
	}
	buf := make([]byte, 0, n)
	for _, l := range ls {
		buf = append(buf, l.Text...)
		buf = append(buf, l.End...)
	}
	return buf
}
