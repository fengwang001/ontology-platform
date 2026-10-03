// Package lines splits bytes into lines, preserving each line's
// original terminator (\n, \r\n, or none for a final line), and
// joins lines back losslessly.
package lines

import "bytes"

// Split divides b into lines; each returned line keeps its ending.
// Empty input yields no lines.
func Split(b []byte) []string {
	var out []string
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, string(b))
			break
		}
		out = append(out, string(b[:i+1]))
		b = b[i+1:]
	}
	return out
}

// Join concatenates lines back into their original byte form.
func Join(ls []string) []byte {
	var b bytes.Buffer
	for _, l := range ls {
		b.WriteString(l)
	}
	return b.Bytes()
}
