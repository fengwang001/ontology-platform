// Package lines splits byte strings into lines that keep their original
// terminators, and joins them back losslessly.
package lines

import "bytes"

// Split divides b into lines. Each line keeps its trailing "\n" (a "\r\n"
// line therefore keeps its "\r\n"); the final line may have no terminator.
// Empty input yields zero lines. Join(Split(b)) == b always holds.
func Split(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			return append(out, b)
		}
		out = append(out, b[:i+1])
		b = b[i+1:]
	}
	return out
}

// Join concatenates lines back into the original byte string.
func Join(ls [][]byte) []byte {
	return bytes.Join(ls, nil)
}

// Equal reports whether two lines are byte-identical, terminator included,
// so "x" and "x\n" (and "x\r\n" vs "x\n") compare unequal.
func Equal(a, b []byte) bool {
	return bytes.Equal(a, b)
}
