// Package cell describes a single CSV field value.
package cell

// Cell is one field: its decoded value, whether it was quoted in the source,
// and its half-open source byte span [Start, End).
//
// An unquoted empty field and a quoted empty field ("") are distinct:
// they share Value "" but differ in Quoted and in End-Start.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal reports field-by-field equality including the quote marker.
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted &&
		c.Start == o.Start && c.End == o.End
}

// EqualValue ignores byte offsets (used by the writer round-trip check).
func (c Cell) EqualValue(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted
}
