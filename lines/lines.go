// Package lines splits byte strings into lines preserving original endings.
package lines

// Line is one source line with its original terminator.
type Line struct {
	Text []byte // content without terminator
	NL   []byte // terminator: "\n", "\r\n", or nil for the final line
}

// Split cuts data into Lines, preserving "\n", "\r\n", and a missing final
// newline. Empty data yields no lines.
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var out []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			out = append(out, Line{Text: append([]byte(nil), data...)})
			break
		}
		text, nl := data[:i], []byte{'\n'}
		if i > 0 && text[i-1] == '\r' {
			text, nl = text[:i-1], []byte{'\r', '\n'}
		}
		out = append(out, Line{Text: append([]byte(nil), text...), NL: nl})
		data = data[i+1:]
	}
	return out
}

// Join reconstructs the exact original bytes.
func Join(ls []Line) []byte {
	var out []byte
	for _, l := range ls {
		out = append(out, l.Text...)
		out = append(out, l.NL...)
	}
	return out
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
