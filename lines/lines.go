// Package lines splits byte strings into lines while preserving each line's
// original terminator, and joins them back losslessly.
package lines

import "bytes"

// Split divides b into lines. Every line keeps its original ending:
// "\n", "\r\n", or no terminator at all for the final line.
// An empty input yields zero lines.
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

// Join concatenates the given lines back into a single byte string.
func Join(ls [][]byte) []byte {
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

// Terminated reports whether l ends with a newline.
func Terminated(l []byte) bool {
	return len(l) > 0 && l[len(l)-1] == '\n'
}
