// Package lines splits bytes into lines, preserving each line's
// original terminator, and joins them back losslessly.
package lines

import "bytes"

// Split cuts b into lines; each line keeps its trailing "\n" (or "\r\n"),
// except a final line without terminator. Split(nil) returns nil.
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

// Join concatenates lines back into their original byte form.
func Join(ls [][]byte) []byte {
	return bytes.Join(ls, nil)
}
