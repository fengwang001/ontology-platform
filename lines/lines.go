// Package lines splits byte strings into lines preserving original terminators.
package lines

// Line is one line: Content excludes the terminator; NL is the terminator
// ("\n", "\r\n", or empty for a final unterminated line).
type Line struct {
	Content []byte
	NL      []byte
}

// Split cuts data into Lines and round-trips via Join. Empty data yields nil.
func Split(data []byte) []Line {
	var out []Line
	for i := 0; i < len(data); {
		j := indexNL(data[i:])
		if j < 0 {
			out = append(out, Line{Content: append([]byte(nil), data[i:]...)})
			break
		}
		end := i + j
		nl := []byte{'\n'}
		if j > 0 && data[end-1] == '\r' {
			end--
			nl = []byte{'\r', '\n'}
		}
		c := append([]byte(nil), data[i:end]...)
		out = append(out, Line{Content: c, NL: nl})
		i = i + j + 1
	}
	return out
}

func indexNL(s []byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return i
		}
	}
	return -1
}

// Raw returns content plus its terminator.
func (l Line) Raw() []byte { return append(append([]byte(nil), l.Content...), l.NL...) }

// Join reconstructs the original bytes exactly.
func Join(ls []Line) []byte {
	var b []byte
	for _, l := range ls {
		b = append(b, l.Raw()...)
	}
	return b
}

// Equal compares content and terminator byte-for-byte.
func (l Line) Equal(o Line) bool {
	return string(l.Content) == string(o.Content) && string(l.NL) == string(o.NL)
}

// NoNL reports whether this is a final line without a terminator.
func (l Line) NoNL() bool { return len(l.NL) == 0 }
