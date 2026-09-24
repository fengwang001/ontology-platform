// Package lines splits byte strings into logical lines while preserving each
// line's original terminator (\n, \r\n, or none for the final line).
package lines

// Line is one logical line. Content excludes the terminator; Term is the
// original terminator ("\n", "\r\n", or "" when the file has no final newline).
type Line struct {
	Content []byte
	Term    []byte
}

// Split divides data into lines, preserving terminators exactly.
// Empty input yields no lines; a trailing newline terminates the preceding
// line rather than producing an extra empty one.
func Split(data []byte) []Line {
	var out []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			out = append(out, Line{Content: append([]byte(nil), data...)})
			break
		}
		end := i
		var term []byte
		if i > 0 && data[i-1] == '\r' {
			end = i - 1
			term = []byte("\r\n")
		} else {
			term = []byte("\n")
		}
		out = append(out, Line{
			Content: append([]byte(nil), data[:end]...),
			Term:    term,
		})
		data = data[i+1:]
	}
	return out
}

// Join rebuilds the exact bytes the lines were split from.
func Join(ls []Line) []byte {
	var buf []byte
	for _, l := range ls {
		buf = append(buf, l.Content...)
		buf = append(buf, l.Term...)
	}
	return buf
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Equal reports whether two line sequences are byte-identical (content and
// terminator both matter).
func Equal(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i].Content) != string(b[i].Content) ||
			string(a[i].Term) != string(b[i].Term) {
			return false
		}
	}
	return true
}

// Clone returns a deep copy of a line slice.
func Clone(ls []Line) []Line {
	out := make([]Line, len(ls))
	for i, l := range ls {
		out[i] = Line{
			Content: append([]byte(nil), l.Content...),
			Term:    append([]byte(nil), l.Term...),
		}
	}
	return out
}
