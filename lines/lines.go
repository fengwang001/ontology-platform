// Package lines splits byte strings into lines while preserving each
// line's original terminator ("\n", "\r\n", or none on the last line),
// and joins them back losslessly.
package lines

import "bytes"

var nl = []byte("\n")

// Split cuts b into lines; every line keeps its trailing "\n" (a "\r\n"
// line therefore ends in "\r\n") except possibly the last one.
// Empty input yields no lines.
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

// HasEOL reports whether l ends with a newline.
func HasEOL(l []byte) bool { return bytes.HasSuffix(l, nl) }
