package ontology

// Semantics selects how duplicate values are treated by an operation.
type Semantics int

const (
	// Set deduplicates: each distinct value appears at most once.
	Set Semantics = iota
	// Multiset keeps multiplicities: Union takes per-stream maximum
	// counts, Intersect minimum counts, Difference subtracts counts.
	Multiset
)

func (s Semantics) String() string {
	switch s {
	case Set:
		return "Set"
	case Multiset:
		return "Multiset"
	default:
		return "Semantics(?)"
	}
}
