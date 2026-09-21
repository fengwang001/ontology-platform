package streamset

// Semantics selects how repeated values are treated in results.
type Semantics int

const (
	// Set collapses duplicates: each distinct value appears at most once.
	Set Semantics = iota
	// Multiset keeps multiplicities: union takes per-value maximum counts,
	// intersection takes minimum counts, difference subtracts counts.
	Multiset
)

func (s Semantics) String() string {
	if s == Multiset {
		return "multiset"
	}
	return "set"
}

// Op identifies a set operation over ordered streams.
type Op int

const (
	// OpUnion emits values present in any stream.
	OpUnion Op = iota
	// OpIntersect emits values present in every stream.
	OpIntersect
	// OpDifference emits values of the first stream not cancelled by the
	// remaining streams.
	OpDifference
)

func (o Op) String() string {
	switch o {
	case OpIntersect:
		return "intersect"
	case OpDifference:
		return "difference"
	default:
		return "union"
	}
}
