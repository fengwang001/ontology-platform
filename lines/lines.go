// Package lines splits byte strings into lines, preserving each line's
// original terminator (\n, \r\n, or none for a final unterminated line),
// and joins them back losslessly.
package lines

import "bytes"

// Split cuts b into lines. Every line keeps its trailing "\n" if present;
// only the final line may lack one. Split(nil) and Split(empty) yield no lines.
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

// Body returns the line content without the trailing "\n".
// A "\r" of a CRLF terminator stays part of the body.
func Body(l []byte) []byte {
	return bytes.TrimSuffix(l, []byte("\n"))
}

// HasNL reports whether the line ends with "\n".
func HasNL(l []byte) bool {
	return len(l) > 0 && l[len(l)-1] == '\n'
}
