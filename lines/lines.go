// Package lines splits byte strings into lines while preserving each line's
// original ending ("\n", "\r\n", or none for the final line).
package lines

// Line is one logical line. Data excludes the ending; EOL is "\n", "\r\n",
// or nil when the line is the final line without a newline.
type Line struct {
	Data []byte
	EOL  []byte
}

// Split divides b into lines, retaining original endings. An empty input
// yields zero lines.
func Split(b []byte) []Line {
	var out []Line
	for len(b) > 0 {
		i := indexByte(b, '\n')
		if i < 0 {
			out = append(out, copyLine(b, nil))
			break
		} else {
			end := []byte{'\n'}
			data := b[:i]
			if i > 0 && data[i-1] == '\r' {
				data = data[:i-1]
				end = []byte{'\r', '\n'}
			}
			out = append(out, copyLine(data, end))
			b = b[i+1:]
		}
	}
	return out
}

func copyLine(data, eol []byte) Line {
	d := make([]byte, len(data))
	copy(d, data)
	var e []byte
	if eol != nil {
		e = make([]byte, len(eol))
		copy(e, eol)
	}
	return Line{Data: d, EOL: e}
}

// Join reconstructs the exact byte sequence represented by ls.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Data) + len(l.EOL)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Data...)
		out = append(out, l.EOL...)
	}
	return out
}

// Equal reports whether two lines are byte-identical including endings.
func Equal(a, b Line) bool {
	return bytesEqual(a.Data, b.Data) && bytesEqual(a.EOL, b.EOL)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func indexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}
