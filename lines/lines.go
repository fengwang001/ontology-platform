// Package lines splits byte strings into lines while preserving each
// line's original terminator ("\n", "\r\n", or none for the last line).
package lines

import "bytes"

// Split returns the lines of b. Every line except possibly the last
// keeps its trailing '\n' (and therefore a preceding '\r'). Join is the
// inverse: Join(Split(b)) == b for every b, including b == nil.
func Split(b []byte) [][]byte {
	var out [][]byte
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

// Join concatenates lines exactly as Split produced them.
func Join(ls [][]byte) []byte {
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
