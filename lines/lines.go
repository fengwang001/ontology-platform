// Package lines splits byte strings into lines while preserving each original
// line terminator (\n or \r\n), including a final line without a terminator.
package lines

// Line is one logical line: Text excludes the terminator, Term is "\n",
// "\r\n", or "" for a final line missing a newline.
type Line struct {
	Text string
	Term string
}

// Split cuts data into Lines. Empty input yields no lines.
func Split(data []byte) []Line {
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		term := "\n"
		textEnd := i
		if i > start && data[i-1] == '\r' {
			term = "\r\n"
			textEnd = i - 1
		}
		out = append(out, Line{Text: string(data[start:textEnd]), Term: term})
		start = i + 1
	}
	if start < len(data) {
		out = append(out, Line{Text: string(data[start:]), Term: ""})
	}
	return out
}

// SplitString is Split for strings.
func SplitString(s string) []Line {
	return Split([]byte(s))
}

// Join concatenates lines back to the exact original bytes.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.Term)
	}
	buf := make([]byte, 0, n)
	for _, l := range ls {
		buf = append(buf, l.Text...)
		buf = append(buf, l.Term...)
	}
	return buf
}

// JoinString is Join returning a string.
func JoinString(ls []Line) string {
	return string(Join(ls))
}

// NoNewline reports whether l is the final line without a terminator.
func (l Line) NoNewline() bool { return l.Term == "" }
