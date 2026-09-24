// Package lines splits byte text into logical lines while preserving each
// line's original terminator ("\n", "\r\n", or none for the final line).
package lines

// Line is one logical line. Text excludes the terminator; EOL is the raw
// terminator bytes ("\n", "\r\n", or nil when the final line has none).
type Line struct {
	Text []byte
	EOL  []byte
}

// Bytes returns the raw line including its terminator.
func (l Line) Bytes() []byte {
	out := make([]byte, 0, len(l.Text)+len(l.EOL))
	out = append(out, l.Text...)
	out = append(out, l.EOL...)
	return out
}

// Split cuts data into Lines. Split(nil) and Split([]byte{}) yield no lines.
func Split(data []byte) []Line {
	var ls []Line
	for i := 0; i < len(data); {
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		text, eol := data[i:j], []byte(nil)
		if j < len(data) {
			end := j
			if end > i && data[end-1] == '\r' {
				end--
			}
			text, eol = data[i:end], data[end:j+1]
		}
		ls = append(ls, Line{Text: append([]byte(nil), text...), EOL: append([]byte(nil), eol...)})
		i = j + 1
	}
	return ls
}

// Join concatenates lines back into the exact original bytes.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Text) + len(l.EOL)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Text...)
		out = append(out, l.EOL...)
	}
	return out
}

// Equal reports whether two lines have identical text and terminator.
func Equal(a, b Line) bool {
	return string(a.Text) == string(b.Text) && string(a.EOL) == string(b.EOL)
}

// NoNL is the unified-diff marker emitted after a final line lacking EOL.
var NoNL = []byte("\\ No newline at end of file")
