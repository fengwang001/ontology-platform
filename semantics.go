package ontology

// Semantics selects how multiplicities are treated.
type Semantics int

const (
	// Set collapses duplicates: each distinct value appears at most once.
	Set Semantics = iota
	// Multiset keeps multiplicities: Union takes per-stream maxima,
	// Intersect minima, Difference a floored subtraction.
	Multiset
)

func (s Semantics) String() string {
	switch s {
	case Set:
		return "set"
	case Multiset:
		return "multiset"
	default:
		return "unknown"
	}
}

// Op selects the set operation to compute.
type Op int

const (
	// OpUnion emits values present in at least one stream.
	OpUnion Op = iota
	// OpIntersect emits values present in every stream.
	OpIntersect
	// OpDifference emits values of the first stream not covered by the rest.
	OpDifference
)

func (o Op) String() string {
	switch o {
	case OpUnion:
		return "union"
	case OpIntersect:
		return "intersect"
	case OpDifference:
		return "difference"
	default:
		return "unknown"
	}
}

// Stats reports resource usage of one merge pass.
type Stats struct {
	// Comparisons is the number of element comparisons the heap
	// performed while merging. It is O(n log k) for n total elements
	// and k streams, hence linear in n for a fixed k; the concrete
	// upper bound is 3*n*ceil(log2(k)) (zero when k < 2).
	Comparisons int
	// MaxHeapSize is the largest number of elements the merge heap
	// held at any moment. It never exceeds the number of streams.
	MaxHeapSize int
}
