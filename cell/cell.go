// Package cell represents one CSV field and its source span.
package cell

// Cell is a field value with its original quoting and byte span.
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Clone returns a deep copy so callers cannot mutate parser state.
func (c Cell) Clone() Cell {
	c.Value = append([]byte(nil), c.Value...)
	return c
}
