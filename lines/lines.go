// Package lines splits byte strings into lines while preserving line endings.
package lines

// Line is one logical line; Text excludes the original terminator,
// NL is the preserved terminator ("\n" or "\r\n"), empty for the final line.
type Line struct {
	Text []byte
	NL   []byte
}

// Split divides data into lines preserving each terminator.
func Split(data []byte) []Line {
	return nil
}

// Join reconstructs the exact original bytes.
func Join(ls []Line) []byte {
	return nil
}
