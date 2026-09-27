// Package cell represents one CSV field value with its quote mark and
// original byte offsets. It depends on no other package in this module.
package cell

// Cell is a single parsed field.
//
// Quoted distinguishes an unquoted empty field (Quoted==false, Value=="")
// from a quoted empty field written as "" (Quoted==true, Value=="").
// Start/End are byte offsets in the original stream: Start is the offset
// of the field's first byte (the opening quote when quoted), End is the
// offset just past the field's last content byte (the closing quote when
// quoted). For a zero-byte empty field Start==End.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal reports whether two cells are equal in value, quote mark and offsets.
func (c Cell) Equal(o Cell) bool {
	return c == o
}
