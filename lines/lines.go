// Package lines splits byte strings into lines that keep their original
// line terminators, and joins them back losslessly.
package lines

// Split divides b into lines. Each returned line includes its original
// terminator ("\n" or "\r\n"); the last line may have none. Split(nil)
// returns nil. Join(Split(b)) == b always holds.
func Split(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := 0
		for i < len(b) && b[i] != '\n' {
			i++
		}
		if i < len(b) {
			i++
		}
		out = append(out, b[:i])
		b = b[i:]
	}
	return out
}

// Join concatenates lines back into their original byte string.
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
