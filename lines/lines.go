// Package lines splits byte content into lines preserving original line
// terminators ("\n", "\r\n", or no terminator on the last line).
package lines

// Split cuts b into lines. Each line keeps its original terminator; the final
// line has none when b does not end in "\n". Empty b yields no lines.
func Split(b []byte) [][]byte {
	var out [][]byte
	for i := 0; i < len(b); {
		j := i
		for j < len(b) && b[j] != '\n' {
			j++
		}
		if j < len(b) {
			j++
		}
		out = append(out, b[i:j])
		i = j
	}
	return out
}

// Join concatenates lines back into the exact original byte content.
func Join(ls [][]byte) []byte {
	n := 0
	for _, l := range ls {
		n += len(l)
	}
	b := make([]byte, 0, n)
	for _, l := range ls {
		b = append(b, l...)
	}
	return b
}

// Content returns the line without its trailing "\n" or "\r\n".
func Content(l []byte) []byte {
	if len(l) > 0 && l[len(l)-1] == '\n' {
		l = l[:len(l)-1]
	}
	if len(l) > 0 && l[len(l)-1] == '\r' {
		l = l[:len(l)-1]
	}
	return l
}

// HasTerm reports whether l ends in a newline terminator.
func HasTerm(l []byte) bool {
	return len(l) > 0 && l[len(l)-1] == '\n'
}
