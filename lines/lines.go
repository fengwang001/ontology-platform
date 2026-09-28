// Package lines splits byte strings into lines preserving original line
// endings ("\n", "\r\n", or no ending on the final line) and joins them back
// byte-for-byte. It depends on no other package.
package lines

// Line is one line: Text excludes the line ending; EOL is "\n", "\r\n", or nil
// for the final line without a line ending.
type Line struct {
	Text []byte
	EOL  []byte
}

// Split cuts data into lines, retaining every original line ending.
func Split(data []byte) []Line {
	ls := []Line{}
	for start := 0; start < len(data); {
		i := start
		for i < len(data) && data[i] != '\n' {
			i++
		}
		if i < len(data) { // found '\n'
			textEnd := i
			eol := data[i : i+1]
			if textEnd > start && data[textEnd-1] == '\r' {
				textEnd--
				eol = data[textEnd : i+1]
			}
			ls = append(ls, Line{Text: data[start:textEnd], EOL: eol})
			start = i + 1
		} else { // final line without a line ending (non-empty remainder only)
			ls = append(ls, Line{Text: data[start:], EOL: nil})
			start = len(data)
		}
	}
	return ls
}

// Join reconstructs the exact original byte string.
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
