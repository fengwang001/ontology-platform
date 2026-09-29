// Package cell describes a single CSV field value and its source span.
package cell

// Cell is one parsed field. Start/End are byte offsets into the original
// input: Start is the index of the field's first byte, End is one past its
// last byte (End==Start for an unquoted empty field).
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Equal reports whether two cells have identical value and quote marker.
func Equal(a, b Cell) bool {
	return a.Quoted == b.Quoted && string(a.Value) == string(b.Value)
}
