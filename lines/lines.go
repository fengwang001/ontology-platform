// Package lines splits byte content into logical lines keeping original
// terminators (\n, \r\n, or none on the last line) and joins them back.
package lines

// Line is one logical line. Content excludes the terminator; NL is the
// original terminator bytes ("\n" or "\r\n"), empty for a final unterminated
// last line.
type Line struct {
	Content []byte
	NL      []byte
}

// Split cuts data into lines preserving each line's terminator. Empty input
// yields no lines (a missing last line differs from one empty last line).
func Split(data []byte) []Line {
	ls := []Line{}
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			ls = append(ls, Line{Content: data})
			break
		}
		end := i
		nl := []byte{'\n'}
		if end > 0 && data[end-1] == '\r' {
			end--
			nl = []byte{'\r', '\n'}
		}
		ls = append(ls, Line{Content: append([]byte(nil), data[:end]...), NL: nl})
		data = data[i+1:]
	}
	return ls
}

// Join reconstructs the exact original bytes.
func Join(ls []Line) []byte {
	var out []byte
	for _, l := range ls {
		out = append(out, l.Content...)
		out = append(out, l.NL...)
	}
	return out
}

// Equal reports exact equality including terminators.
func Equal(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i].Content) != string(b[i].Content) ||
			string(a[i].NL) != string(b[i].NL) {
			return false
		}
	}
	return true
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
