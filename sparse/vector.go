// Package sparse implements dot product and cosine similarity over
// sparse vectors represented as index-sorted (index, value) sequences.
//
// Vectors are never expanded into dense arrays: both metrics are
// computed with a single two-pointer merge over the inputs.
package sparse

// Entry is one explicit element of a sparse vector.
// Index must be strictly ascending within a Vector; Value may be zero
// (explicit zeros are legal in a sparse representation).
type Entry struct {
	Index uint32
	Value float64
}

// Vector is a sparse vector: entries sorted by strictly ascending Index.
type Vector []Entry

// Stats reports observable facts about a single computation.
type Stats struct {
	// Steps is the number of merge iterations the two-pointer sweep
	// advanced. It is bounded by len(a)+len(b), independent of the
	// magnitude of the indices.
	Steps int
	// ExplicitZeros counts entries whose stored Value is exactly 0
	// across both input vectors. They are legal and do not affect
	// the dot product.
	ExplicitZeros int
}
