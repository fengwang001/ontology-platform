// Package lines splits byte strings into lines preserving original line endings.
package lines

// Line is one line: Text excludes the line ending; End is "\n", "\r\n" or "".
type Line struct {
	Text []byte
	End  []byte
}

// Split cuts s into Lines, keeping each original ending. Empty input has 0 lines.
func Split(s []byte) []Line {
	return nil
}

// Join concatenates lines back exactly as Split found them.
func Join(ls []Line) []byte {
	return nil
}

// Equal reports whether two lines have identical text and ending.
func Equal(x, y Line) bool {
	return false
}
