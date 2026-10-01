// Package lines splits byte strings into lines that keep their original
// terminators and joins them back losslessly.
package lines

import "bytes"

// Split divides b into lines, each keeping its original terminator
// ("\n", "\r\n", or none for a final unterminated line). Empty input
// yields nil. Join(Split(b)) == b always holds.
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
