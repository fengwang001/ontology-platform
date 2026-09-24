// Package cell represents one CSV field value and its source offsets.
package cell

// Cell is a single field. Quoted distinguishes "" from an unquoted empty
// field. Start/End are byte offsets into the original input: Start is the
// first content byte (or the opening quote when quoted) and End is exclusive,
// pointing just past the last content byte (or closing quote).
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Equal reports field-by-field equality including quote marks and offsets.
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted &&
		c.Start == o.Start && c.End == o.End
}
