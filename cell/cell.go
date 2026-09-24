// Package cell represents one CSV field value.
package cell

// Cell is a field: value, quote marker, and absolute byte span.
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}
