// Package lines splits bytes into lines preserving each original line ending.
package lines

// Line is one line: Text without terminator, NL is the original terminator
// ("\n", "\r\n", or "" for a final line lacking a newline).
type Line struct {
	Text string
	NL   string
}

// Split divides data into lines. Empty input yields zero lines; a trailing
// empty line ("\n") yields a Line{"", "\n"}.
func Split(data []byte) []Line {
	return nil
}

// Join reconstructs the exact original bytes.
func Join(ls []Line) []byte {
	return nil
}
