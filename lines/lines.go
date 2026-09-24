// Package lines splits byte strings into lines that keep their original
// terminators, and joins them back losslessly.
package lines

import "bytes"

// Line is one line of a file, including its trailing "\n" or "\r\n"
// terminator if present. The last line of a file may have no terminator.
type Line []byte

// Split cuts b into lines, preserving each line's original terminator.
// Join(Split(b)) == b holds for any b.
func Split(b []byte) []Line {
	var out []Line
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, b)
			break
		}
		out = append(out, b[:i+1])
		b = b[i+1:]
	}
	return out
}

// Join concatenates lines back into the original byte string.
func Join(ls []Line) []byte {
	var out []byte
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

// Equal reports whether two lines are byte-identical, terminator included.
func Equal(a, b Line) bool {
	return bytes.Equal(a, b)
}

// HasTerm reports whether the line ends with a "\n" terminator.
func HasTerm(l Line) bool {
	return len(l) > 0 && l[len(l)-1] == '\n'
}
