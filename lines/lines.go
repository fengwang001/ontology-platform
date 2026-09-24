// Package lines splits byte strings into lines while preserving each line's
// original terminator ("\n", "\r\n", or none for the final line).
package lines

import "bytes"

// Line is one input line; Data includes the original line terminator (if any).
type Line struct {
	Data []byte
}

// Split cuts b into lines. Each line keeps its terminator. The empty input
// produces zero lines, so Join(Split(b)) == b for every b including b == nil.
func Split(b []byte) []Line {
	var out []Line
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, Line{Data: append([]byte(nil), b...)})
			break
		}
		out = append(out, Line{Data: append([]byte(nil), b[:i+1]...)})
		b = b[i+1:]
	}
	return out
}

// Join reconstructs the exact original byte sequence.
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

// Term returns the line terminator ("\n" or "\r\n") or nil when the line is
// the final line of its file and has no terminator.
func (l Line) Term() []byte {
	if len(l.Data) == 0 || l.Data[len(l.Data)-1] != '\n' {
		return nil
	}
	if len(l.Data) >= 2 && l.Data[len(l.Data)-2] == '\r' {
		return l.Data[len(l.Data)-2:]
	}
	return l.Data[len(l.Data)-1:]
}

// HasNL reports whether the line ends with a newline.
func (l Line) HasNL() bool { return l.Term() != nil }

// Content returns the line without its terminator.
func (l Line) Content() []byte {
	if t := l.Term(); t != nil {
		return l.Data[:len(l.Data)-len(t)]
	}
	return l.Data
}
