// Package lines splits byte strings into lines while preserving each line's
// original terminator, so Join(Split(b)) == b for every byte string.
package lines

// Line is one physical line. Content excludes the terminator; End is "\n",
// "\r\n" or "" (only possible for the final line of the input).
type Line struct {
	Content string
	End     string
}

// Split cuts data into lines. Empty input yields no lines.
func Split(data []byte) []Line {
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i
		term := "\n"
		if i > 0 && data[i-1] == '\r' {
			end = i - 1
			term = "\r\n"
		}
		out = append(out, Line{Content: string(data[start:end]), End: term})
		start = i + 1
	}
	if start < len(data) {
		out = append(out, Line{Content: string(data[start:]), End: ""})
	}
	return out
}

// Join reconstructs the exact byte string the lines were split from.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Content) + len(l.End)
	}
	buf := make([]byte, 0, n)
	for _, l := range ls {
		buf = append(buf, l.Content...)
		buf = append(buf, l.End...)
	}
	return buf
}

// NoEnd reports whether l is a final line without a terminator.
func (l Line) NoEnd() bool { return l.End == "" }
