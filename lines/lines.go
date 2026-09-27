// Package lines splits byte strings into lines that keep their original
// terminators and joins them back losslessly.
package lines

import "strings"

// Split cuts b into lines. Every line keeps its trailing "\n" (or "\r\n"),
// except a final line without terminator. Split(nil) returns nil.
func Split(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, string(b[start:i+1]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}

// Join concatenates lines back into their original byte string.
func Join(ls []string) []byte {
	return []byte(strings.Join(ls, ""))
}
