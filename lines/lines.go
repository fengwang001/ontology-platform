// Package lines splits bytes into newline-preserving lines and joins them back.
package lines

// Line is one text line. Text excludes the line terminator; CRLF reports \r\n.
type Line struct {
	Text []byte
	CRLF bool
	NL   bool // true if the line is terminated by a newline
}

// Split slices data into lines, keeping every original line terminator.
func Split(data []byte) []Line {
	ls := []Line{}
	for i := 0; i < len(data); {
		j := i
		for j < len(data) && data[j] != '\n' {
			j++
		}
		if j == len(data) {
			ls = append(ls, Line{Text: data[i:j], NL: false})
			break
		}
		text := data[i:j]
		crlf := false
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			crlf = true
		}
		ls = append(ls, Line{Text: text, CRLF: crlf, NL: true})
		i = j + 1
	}
	return ls
}

// Join reconstructs the exact byte sequence Split consumed.
func Join(ls []Line) []byte {
	var b []byte
	for _, l := range ls {
		b = append(b, l.Text...)
		if l.NL {
			if l.CRLF {
				b = append(b, '\r')
			}
			b = append(b, '\n')
		}
	}
	return b
}

// Key is the content identity of a line, ignoring its terminator style.
func Key(l Line) string { return string(l.Text) }
