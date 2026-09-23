// Package lines splits byte strings into lines preserving original line
// endings (\n, \r\n, or a missing final newline) and joins them back.
package lines

// Line is one line: Text excludes the terminator; NL is the original
// terminator ("\n", "\r\n", or "" for a final unterminated line).
type Line struct {
	Text string
	NL   string
}

// Split divides data into Lines without copying semantics beyond strings.
func Split(data []byte) []Line {
	return nil
}

// Join concatenates lines exactly as they were split.
func Join(ls []Line) []byte {
	return nil
}

// String returns the reconstructed file content.
func String(ls []Line) string {
	return ""
}

// Equal reports whether two lines have identical text and terminator.
func Equal(x, y Line) bool {
	return false
}
