// Package lines splits byte strings into lines while preserving each line's
// original terminator (\n, \r\n, or none for the final line).
package lines

// Split returns the lines of b. Every element includes its terminator:
// "\n", "\r\n", except possibly the final element when b does not end with
// a newline. The empty input produces no lines. Join(Split(b)) == b.
func Split(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		end := i + 1
		out = append(out, b[start:end:end])
		start = end
	}
	if start < len(b) {
		out = append(out, b[start:len(b):len(b)])
	}
	return out
}

// Join concatenates lines without altering them.
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

// HasNL reports whether the line carries its terminator.
func HasNL(l []byte) bool { return len(l) > 0 && l[len(l)-1] == '\n' }

// Body returns the line without its terminator (\r\n and \n are both removed).
func Body(l []byte) []byte {
	if n := len(l); n > 0 && l[n-1] == '\n' {
		l = l[:n-1]
		if n := len(l); n > 0 && l[n-1] == '\r' {
			l = l[:n-1]
		}
	}
	return l
}
