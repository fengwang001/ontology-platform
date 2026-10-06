// Package sourcemap composes sparse generated-to-source mappings.
//
// All coordinates and source indices use uint64 while preserving the logical
// domain [0,10^9].
package sourcemap

const MaxCoordinate uint64 = 1_000_000_000

// Segment starts at GeneratedColumn and either maps or is explicitly unmapped.
type Segment struct {
	GeneratedColumn uint64
	Mapped          bool
	SourceIndex     uint64
	SourceLine      uint64
	SourceColumn    uint64
}

// Row is one generated line's strictly increasing segment sequence.
type Row struct {
	GeneratedLine uint64
	Segments      []Segment
}

// Mapping is a set of sparse generated rows with SourceCount source indices.
type Mapping struct {
	SourceCount uint64
	Rows        []Row
}

// SourcePosition identifies one column in one original source.
type SourcePosition struct {
	SourceIndex  uint64
	SourceLine   uint64
	SourceColumn uint64
}

// LookupResult reports whether a generated position has a source position.
type LookupResult struct {
	Mapped bool
	SourcePosition
}

type indexRow struct {
	line     uint64
	segments []Segment
}

// IndexedMapping is an immutable canonical mapping with binary-search lookup.
type IndexedMapping struct {
	sourceCount uint64
	rows        []indexRow
}
