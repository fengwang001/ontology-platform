// Package cell represents one CSV field value.
package cell

// Cell is a decoded field.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Empty reports whether the field carries no value bytes.
func (c Cell) Empty() bool { return len(c.Value) == 0 }
