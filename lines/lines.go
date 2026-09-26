// Package lines splits byte strings into lines while preserving each
// line's original ending, and joins them back losslessly.
package lines

// Split divides b into lines. Every line keeps its original ending
// ("\n", possibly preceded by "\r"); the last line may have none.
// An empty input yields no lines.
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

// Join concatenates lines back into the exact original byte string.
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
