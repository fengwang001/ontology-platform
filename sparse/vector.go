// Package sparse provides dot product and cosine similarity for sparse
// vectors represented as index-ordered (index, value) sequences.
//
// Vectors are never expanded into dense arrays: the dot product is a
// single two-pointer merge over the two index sequences. Inputs are
// never modified (no in-place sorting) and no state is shared between
// calls, so concurrent computations are safe.
package sparse

// Element is one explicit entry of a sparse vector. Index must be
// strictly increasing across a Vector. A zero Value is legal (sparse
// formats may store explicit zeros) and is counted in Stats but does
// not affect the dot product.
type Element struct {
	Index uint32
	Value float64
}

// Vector is a sparse vector: elements sorted by strictly increasing
// Index. It is treated as read-only by all functions in this package.
type Vector []Element

// Stats reports observable facts about one computation.
type Stats struct {
	// Steps is the number of merge iterations the two-pointer sweep
	// advanced. It is bounded by len(a)+len(b), independent of the
	// magnitude of the indices.
	Steps int
	// ExplicitZeros counts elements with Value == 0 across both input
	// vectors.
	ExplicitZeros int
}
