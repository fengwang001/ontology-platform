package cell

// Cell describes one CSV field and its exact source span.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted &&
		c.Start == o.Start && c.End == o.End
}
