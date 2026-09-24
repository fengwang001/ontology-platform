package cell

// Cell is one decoded field and its raw byte span.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// New creates a field. End is exclusive and covers quotes for quoted fields.
func New(value string, quoted bool, start, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}

// Equal compares decoded value and quote mark.
func (c Cell) Equal(other Cell) bool {
	return c.Value == other.Value && c.Quoted == other.Quoted
}
