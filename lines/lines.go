// Package lines splits bytes into lines while preserving each line's
// original terminator, and joins them back losslessly.
package lines

import "bytes"

// Split divides b into lines. Every line keeps its original terminator
// ("\n" or "\r\n"); the last line may have no terminator. Empty input
// yields zero lines.
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

// Join concatenates lines back into the original byte sequence.
func Join(ls [][]byte) []byte {
	return bytes.Join(ls, nil)
}
