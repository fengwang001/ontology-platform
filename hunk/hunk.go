package hunk

import "ontology/edit"

// Item is one rendered row inside a Hunk: Kind is edit.OpKind; NoEOL marks the
// "\ No newline at end of file" row that follows this line.
type Item struct {
	Kind  edit.OpKind
	Old   string // full bytes for Equal/Delete
	New   string // full bytes for Equal/Insert
	NoEOL bool
}

// Hunk is one contiguous @@ region.
type Hunk struct {
	OldStart int // 1-based start of old span; previous line number when OldCount==0
	OldCount int
	NewStart int
	NewCount int
	Items   []Item
}

// Build groups a script into hunks using C context lines, merging neighboring
// hunks separated by at most 2*C equal lines.
func Build(s *edit.Script, C int) []*Hunk {
	return nil
}
