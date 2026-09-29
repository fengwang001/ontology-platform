package lines

// Line is one logical line including its original terminator.
// Text is the content without EOL; EOL is "\n", "\r\n", or "" (final line
// without a trailing newline).
type Line struct {
	Text string
	EOL  string
}

// Bytes returns the line exactly as it appeared.
func (l Line) Bytes() string { return l.Text + l.EOL }

// Split cuts data into Lines, preserving each line's original terminator.
func Split(data string) []Line {
	return nil
}

// Join reconstructs the original byte sequence.
func Join(ls []Line) string {
	return ""
}
