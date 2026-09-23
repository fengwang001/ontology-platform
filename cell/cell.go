// Package cell represents one decoded CSV field and its source span.
package cell

// Cell is a field value. Quoted distinguishes "" from an unquoted empty field.
// Start and End are a half-open byte range in the original CSV input.
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Clone returns an independent copy of the cell value.
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	c.Value = v
	return c
}

// Equal reports whether two cells have equal values and quote marks.
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && string(c.Value) == string(o.Value)
}
