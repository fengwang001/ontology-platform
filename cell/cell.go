package cell

// Cell is one CSV field with its original byte extent and quote marker.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Row is one emitted CSV record.
type Row []Cell

// Table is an ordered sequence of records.
type Table struct {
	Rows []Row
}
