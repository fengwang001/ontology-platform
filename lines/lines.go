// Package lines splits byte strings into lines that keep their original
// terminators, and joins them back losslessly.
package lines

import "bytes"

// Line is one line of text including its trailing "\n" or "\r\n",
// except the last line of input which may have no terminator.
type Line []byte

// Split cuts b into lines. Each returned Line shares memory with b.
// Empty input yields no lines; "a\n" yields ["a\n"], "a" yields ["a"].
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
	var n int
	for _, l := range ls {
		n += len(l)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

// HasEOL reports whether l ends with a newline terminator.
func HasEOL(l Line) bool {
	return len(l) > 0 && l[len(l)-1] == '\n'
}
