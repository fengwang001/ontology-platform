// Package lines splits byte strings into lines while preserving original endings.
package lines

// NewLine builds a Line from text and terminator.
func NewLine(text, eol []byte) Line { return Line{Text: text, EOL: eol} }

// Line is one logical line: Text excludes the terminator, EOL is the raw
// terminator ("\n" or "\r\n") or nil for the final line without one.
type Line struct {
	Text []byte
	EOL  []byte
}

// Split divides data into lines keeping each line's original ending.
// "\r\n" is kept as one terminator; a trailing unterminated line keeps EOL nil.
func Split(data []byte) []Line {
	var ls []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			ls = append(ls, Line{Text: append([]byte(nil), data...)})
			break
		}
		text := data[:i]
		eol := data[i : i+1]
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			eol = data[i-1 : i+1]
		}
		ls = append(ls, Line{Text: append([]byte(nil), text...), EOL: append([]byte(nil), eol...)})
		data = data[i+1:]
	}
	return ls
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Join reconstructs the exact original bytes from lines.
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
