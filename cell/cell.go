package cell

// Cell is one CSV field. Offsets are byte offsets in the original input.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal reports whether two cells have equal value and quote marker.
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted
}

// Record is one assembled CSV record.
type Record []Cell

// Equal compares values and quote markers only.
func (r Record) Equal(o Record) bool {
	if len(r) != len(o) {
		return false
	}
	for i := range r {
		if !r[i].Equal(o[i]) {
			return false
		}
	}
	return true
}

// Table is all successfully produced records plus the optional header.
type Table struct {
	Header Record
	Rows   []Record
}

// Records returns the header followed by rows when a header exists.
func (t *Table) Records() []Record {
	if t.Header == nil {
		return t.Rows
	}
	out := make([]Record, 0, len(t.Rows)+1)
	out = append(out, t.Header)
	out = append(out, t.Rows...)
	return out
}

// Equal compares all records and their quote markers.
func (t *Table) Equal(o *Table) bool {
	a, b := t.Records(), o.Records()
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}
