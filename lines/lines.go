// Package lines splits byte strings into lines while preserving each
// line's original ending, and joins them back losslessly.
package lines

import "strings"

// Split divides b into lines. Each line keeps its original ending
// ("\n" or "\r\n"); the final line may have no ending at all.
// Join(Split(b)) == b holds for every b.
func Split(b []byte) []string {
	var out []string
	s := string(b)
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// Join concatenates lines back into their original byte form.
func Join(ls []string) []byte {
	return []byte(strings.Join(ls, ""))
}

// HasEOL reports whether l ends with a newline.
func HasEOL(l string) bool {
	return strings.HasSuffix(l, "\n")
}
