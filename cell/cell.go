// Package cell represents a single CSV field value with its source span.
package cell

// Cell is one parsed field. Value is the decoded field content (escaped
// quotes resolved; in-field CR/LF preserved byte-for-byte). Quoted reports
// whether the field was quoted in the source, distinguishing an unquoted
// empty field from a quoted empty one (""). Start and End are byte offsets
// into the original stream; End is exclusive and points past the closing
// quote for quoted fields.
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Clone returns an independent copy of the cell's value bytes.
func (c Cell) Clone() Cell {
	v := make([]byte, len(c.Value))
	copy(v, c.Value)
	return Cell{Value: v, Quoted: c.Quoted, Start: c.Start, End: c.End}
}
