// Package lines splits byte slices into lines while preserving line endings.
package lines

// Line is one textual line including its original terminator: "\n",
// "\r\n", or empty terminator for a final unterminated line.
type Line struct {
	Text string // line content without terminator
	NL   string // terminator: "\n", "\r\n", or ""
}

// Split divides data into lines. Empty input yields no lines.
func Split(data []byte) []Line {
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i
		nl := "\n"
		if i > start && data[i-1] == '\r' {
			end = i - 1
			nl = "\r\n"
		}
		out = append(out, Line{Text: string(data[start:end]), NL: nl})
		start = i + 1
	}
	if start < len(data) {
		out = append(out, Line{Text: string(data[start:]), NL: ""})
	}
	return out
}

// SplitString is Split for strings.
func SplitString(s string) []Line { return Split([]byte(s)) }

// Join reconstructs the exact original bytes.
func Join(ls []Line) []byte {
	total := 0
	for _, l := range ls {
		total += len(l.Text) + len(l.NL)
	}
	buf := make([]byte, 0, total)
	for _, l := range ls {
		buf = append(buf, l.Text...)
		buf = append(buf, l.NL...)
	}
	return buf
}

// JoinString is Join returning a string.
func JoinString(ls []Line) string { return string(Join(ls)) }
