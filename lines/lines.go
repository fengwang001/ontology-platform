// Package lines slices byte strings into lines while preserving the exact
// line terminator of every line ("\n", "\r\n", or no terminator on the
// final line). Lines can be joined back byte-for-byte.
package lines

// Line is one logical line. Data includes the original terminator, if any.
type Line struct {
	Data []byte
}

// Split splits data into lines without copying more than necessary: each
// returned Data is a sub-slice of data.
func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		end := i + 1
		if end-start >= 2 && data[end-2] == '\r' {
			// terminator is "\r\n"; nothing extra to do, both bytes kept
		}
		out = append(out, Line{Data: data[start:end]})
		start = end
	}
	if start < len(data) {
		out = append(out, Line{Data: data[start:]})
	}
	return out
}

// Join concatenates lines back into the original byte string.
func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l.Data)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l.Data...)
	}
	return out
}

// Content returns the line without its terminator ("\n" or "\r\n").
func Content(l Line) []byte {
	d := l.Data
	if len(d) > 0 && d[len(d)-1] == '\n' {
		d = d[:len(d)-1]
		if len(d) > 0 && d[len(d)-1] == '\r' {
			d = d[:len(d)-1]
		}
	}
	return d
}

// HasNL reports whether the line carries a terminator.
func HasNL(l Line) bool {
	return len(l.Data) > 0 && l.Data[len(l.Data)-1] == '\n'
}

// EqualContent reports whether two lines have equal visible content.
func EqualContent(x, y Line) bool {
	return string(Content(x)) == string(Content(y))
}
