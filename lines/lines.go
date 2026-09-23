// Package lines splits byte strings into lines, preserving each line's
// original terminator (\n, \r\n, or none for the final line), and joins
// them back byte-for-byte.
package lines

// Split divides b into lines. Every returned line keeps its trailing '\n'
// except possibly the last one. A '\r' before '\n' stays part of the line
// body, so CRLF terminators survive a Split/Join round trip. Empty input
// yields nil.
func Split(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i+1])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
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

// Body reports the line content without its trailing '\n' and whether
// that terminator was present.
func Body(l []byte) (body []byte, nl bool) {
	if n := len(l); n > 0 && l[n-1] == '\n' {
		return l[:n-1], true
	}
	return l, false
}
