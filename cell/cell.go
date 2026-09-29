// Package cell represents one CSV field value with its quote mark and span.
package cell

// Cell is a decoded field. Quoted distinguishes "" from an unquoted empty
// field. Start/End are byte offsets in the original input; End is exclusive
// and covers the surrounding quotes for quoted fields.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal reports field-level equality including the quote mark and span.
func (c Cell) Equal(o Cell) bool {
	return c == o
}
