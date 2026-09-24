// Package lines splits byte strings into lines while preserving each line's
// original terminator ("\n", "\r\n", or none for the final line).
package lines

// Line is one logical line including its original line ending.
// A final line without a newline has no terminator bytes.
type Line []byte

// Split cuts data into lines. The empty input yields no lines.
func Split(data []byte) []Line {
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			out = append(out, append(Line(nil), data[start:i+1]...))
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, append(Line(nil), data[start:]...))
	}
	return out
}

// Join concatenates lines back into the exact original byte string.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

// HasNL reports whether the line carries a terminator.
func (l Line) HasNL() bool { return len(l) > 0 && l[len(l)-1] == '\n' }

// Content returns the line without its terminator ("\r" of CRLF removed too).
func (l Line) Content() []byte {
	s := l
	if s.HasNL() {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '\r' {
		s = s[:len(s)-1]
	}
	return s
}

// Equal reports byte equality of two lines (terminator included).
func Equal(a, b Line) bool {
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
