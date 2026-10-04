// Package lines splits byte strings into lines that keep their original
// terminators, and joins them back losslessly.
package lines

import "bytes"

// Split divides b into lines, each retaining its original terminator
// ("\n" or "\r\n"); the final line may have no terminator. Empty input
// yields zero lines. Join(Split(b)) is byte-identical to b.
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

// HasNL reports whether l ends with a "\n" terminator.
func HasNL(l []byte) bool {
	return len(l) > 0 && l[len(l)-1] == '\n'
}
