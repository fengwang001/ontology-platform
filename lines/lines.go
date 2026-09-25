// Package lines splits byte strings into lines preserving their original
// line terminators (\n or \r\n) and joins them back byte-for-byte.
package lines

// Line is one logical line. Text excludes the terminator; EOL is the
// original terminator ("\n" or "\r\n") or "" for the final unterminated line.
type Line struct {
	Text string
	EOL  string
}

// Split partitions data into Lines. Empty input yields zero lines.
func Split(data []byte) []Line {
	return split(string(data))
}

// SplitString is the string convenience form of Split.
func SplitString(s string) []Line {
	return split(s)
}

func split(s string) []Line {
	var out []Line
	for len(s) > 0 {
		i := indexByte(s, '\n')
		if i < 0 {
			out = append(out, Line{Text: s, EOL: ""})
			break
		}
		text, eol := s[:i], "\n"
		if len(text) > 0 && text[len(text)-1] == '\r' {
			text = text[:len(text)-1]
			eol = "\r\n"
		}
		out = append(out, Line{Text: text, EOL: eol})
		s = s[i+1:]
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// Join reconstructs the exact byte sequence that produced ls.
func Join(ls []Line) []byte {
	return []byte(JoinString(ls))
}

// JoinString is the string convenience form of Join.
func JoinString(ls []Line) string {
	var b []byte
	for _, l := range ls {
		b = append(b, l.Text...)
		b = append(b, l.EOL...)
	}
	return string(b)
}

// Bytes returns Text plus its original EOL, i.e. the raw line.
func (l Line) Bytes() []byte {
	return []byte(l.Text + l.EOL)
}

// Equal reports whether two lines are byte-identical including EOL.
func (l Line) Equal(o Line) bool {
	return l.Text == o.Text && l.EOL == o.EOL
}
