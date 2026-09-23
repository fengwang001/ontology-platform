// Package lines splits byte strings into logical lines preserving the
// original line terminator ("\n", "\r\n", or absent for the final line).
package lines

// Line is one logical line: Text excludes the terminator, EOL is the
// exact terminator bytes ("\n", "\r\n", or "" for a final unterminated line).
type Line struct {
	Text string
	EOL  string
}

// Split partitions data into lines, keeping every original terminator.
// The empty input produces zero lines.
func Split(data []byte) []Line {
	return nil
}

// Join reconstructs the exact byte sequence the lines were split from.
func Join(ls []Line) []byte {
	return nil
}
