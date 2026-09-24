package lines

// Line is one logical line retaining its original terminator.
type Line struct {
	Text string // content without terminator
	NL   string // "\n", "\r\n", or "" for the last unterminated line
}

// Bytes reconstructs the line exactly.
func (l Line) Bytes() []byte { return []byte(l.Text + l.NL) }

// Split cuts s into Lines, preserving terminators. Empty input yields no lines.
func Split(s []byte) []Line { return nil }

// Join concatenates lines into exact original bytes.
func Join(ls []Line) []byte { return nil }
