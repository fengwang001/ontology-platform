// Package logical assembles physical lines into logical lines per
// java.util.Properties line-termination, comment and continuation rules.
package logical

// Line is one logical line plus the physical origin of every byte.
type Line struct {
	Text []byte
	Phys []Pos // per Text byte: physical line and 1-based column
}

// Pos identifies a byte in the physical input.
type Pos struct {
	Line int
	Col  int
}

// Scan returns logical lines from r. Blank and comment lines are omitted.
func Scan(r interface{ ReadByte() (byte, error) }) ([]Line, error) {
	return nil, nil
}

// Checks reports how many input bytes were inspected by parity/line logic.
func Checks(lines []Line) int { return 0 }
