package cell

// Cell is one decoded CSV field with its source span.
type Cell struct {
	Text   string
	Quoted bool
	Start  int
	End    int
}

// Equal compares decoded text and quoting metadata.
func (c Cell) Equal(other Cell) bool {
	return c.Text == other.Text && c.Quoted == other.Quoted &&
		c.Start == other.Start && c.End == other.End
}
