// Package lines splits byte strings into lines, preserving each line's
// original terminator (\n, \r\n, or none for a final unterminated line),
// and joins them back losslessly.
package lines

import "bytes"

// Split returns the lines of b. Every element except possibly the last
// carries its trailing "\n" (a "\r\n" ending is kept as part of the line).
// Empty input yields nil. The result aliases b.
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

// Join concatenates lines back into their original byte string.
func Join(ls [][]byte) []byte {
	return bytes.Join(ls, nil)
}

// Equal reports whether two lines are byte-identical (terminator included).
func Equal(a, b []byte) bool {
	return bytes.Equal(a, b)
}
