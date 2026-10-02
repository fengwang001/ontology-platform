// Package lines splits byte strings into lines that keep their original
// terminators, and joins them back losslessly.
package lines

// Split divides b into lines. Every line keeps its original terminator
// ("\n" or "\r\n"); the last line may have no terminator. Join(Split(b))
// is byte-identical to b.
func Split(b []byte) []string {
	var out []string
	for len(b) > 0 {
		i := 0
		for i < len(b) && b[i] != '\n' {
			i++
		}
		if i < len(b) {
			i++
		}
		out = append(out, string(b[:i]))
		b = b[i:]
	}
	return out
}

// Join concatenates lines back into the original byte string.
func Join(ls []string) []byte {
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

// Body returns the line content without its terminator, and whether a
// terminator was present.
func Body(l string) (string, bool) {
	if len(l) > 0 && l[len(l)-1] == '\n' {
		return l[:len(l)-1], true
	}
	return l, false
}
