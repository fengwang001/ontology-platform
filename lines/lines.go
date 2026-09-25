// Package lines splits byte strings into lines while preserving each
// line's original terminator, and joins them back losslessly.
package lines

import "bytes"

// Split divides b into lines. Every line keeps its original terminator
// ("\n", possibly preceded by "\r"); the last line may have none.
// Join(Split(b)) is byte-identical to b.
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

// Join concatenates lines back into the original byte string.
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
