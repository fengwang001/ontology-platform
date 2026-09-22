package dict

// IntTable is the read-only inverse of an int64 dictionary: code -> value.
type IntTable struct{ vals []int64 }

// NewIntTable wraps a code-ordered value table.
func NewIntTable(vals []int64) *IntTable { return &IntTable{vals: vals} }

// Cardinal reports the number of distinct values.
func (t *IntTable) Cardinal() int { return len(t.vals) }

// Lookup returns the value behind code c and false for an out-of-range code.
func (t *IntTable) Lookup(c int) (int64, bool) {
	if c < 0 || c >= len(t.vals) {
		return 0, false
	}
	return t.vals[c], true
}

// BytesTable is the read-only inverse of a byte-string dictionary.
type BytesTable struct{ vals [][]byte }

// NewBytesTable wraps a code-ordered value table.
func NewBytesTable(vals [][]byte) *BytesTable { return &BytesTable{vals: vals} }

// Cardinal reports the number of distinct values.
func (t *BytesTable) Cardinal() int { return len(t.vals) }

// Lookup returns the value behind code c and false for an out-of-range code.
func (t *BytesTable) Lookup(c int) ([]byte, bool) {
	if c < 0 || c >= len(t.vals) {
		return nil, false
	}
	return t.vals[c], true
}
