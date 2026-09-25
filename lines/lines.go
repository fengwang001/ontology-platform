// Package lines splits byte strings into lines while preserving each line's
// original terminator ("\n" or "\r\n"; the final line may have none).
package lines

import "strings"

// Split partitions b into lines. Every element retains its terminator,
// except possibly the last element when b does not end with a newline.
// Join(Split(b)) is always byte-identical to b, including for empty b,
// which yields a nil slice.
func Split(b []byte) []string {
	s := string(b)
	var out []string
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

// Join reconstructs the original byte string from lines produced by Split.
func Join(ls []string) []byte {
	return []byte(strings.Join(ls, ""))
}
