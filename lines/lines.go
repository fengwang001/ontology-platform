// Package lines splits byte strings into lines preserving original line
// terminators ("\n", "\r\n", or none for the final line).
package lines

// Line is one physical line: Content excludes the terminator, EOL is the
// original terminator ("\n", "\r\n", or "" for a final unterminated line).
type Line struct {
	Content []byte
	EOL     []byte
}

// Split divides data into physical lines. Empty input yields no lines.
func Split(data []byte) []Line {
	var out []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			out = append(out, Line{Content: data})
			break
		}
		end := i + 1
		if i > 0 && data[i-1] == '\r' {
			out = append(out, Line{Content: data[:i-1], EOL: data[i-1 : end]})
		} else {
			out = append(out, Line{Content: data[:i], EOL: data[i:end]})
		}
		data = data[end:]
	}
	return out
}

// Join reconstructs the exact byte sequence the lines came from.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Content) + len(l.EOL)
	}
	buf := make([]byte, 0, n)
	for _, l := range ls {
		buf = append(buf, l.Content...)
		buf = append(buf, l.EOL...)
	}
	return buf
}

// Full returns the line including its terminator.
func (l Line) Full() []byte {
	return append(append([]byte(nil), l.Content...), l.EOL...)
}

// Equal reports whether two lines are byte-identical, terminator included.
func (l Line) Equal(o Line) bool {
	return string(l.Content) == string(o.Content) && string(l.EOL) == string(o.EOL)
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
