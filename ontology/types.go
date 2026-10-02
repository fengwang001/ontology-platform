package ontology

// Repetition of a schema field.
type Rep int

const (
	Required Rep = iota
	Optional
	Repeated
)

// Field is a schema node. A field without children is an int64 leaf.
type Field struct {
	Name     string
	Rep      Rep
	Children []Field
}

// Entry is one shredded cell of a leaf column.
// Null is true iff Def < the column's maxDef.
type Entry struct {
	Rep   int
	Def   int
	Null  bool
	Value int64
}

// PageInfo describes one page of a column.
type PageInfo struct {
	StartRecord int
	RecordCount int
	EntryCount  int
}

// Stats summarizes a column.
type Stats struct {
	Nulls   int
	Present int
	Min     int64
	Max     int64
}
