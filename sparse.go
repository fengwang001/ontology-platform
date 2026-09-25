// Package sparse provides dot product and cosine similarity for sparse
// vectors, computed with a single two-pointer merge over the sorted
// (index, value) representation. Vectors are never expanded into dense
// arrays, and inputs are never mutated.
package sparse

// Element is one explicit entry of a sparse vector. Elements within a
// Vector must appear in strictly increasing Index order. A zero Value
// is legal (sparse formats may store explicit zeros) and is counted in
// Stats.ExplicitZeros without affecting the dot product.
type Element struct {
	Index uint32
	Value float64
}

// Vector is a sparse vector in sorted (index, value) form.
type Vector struct {
	Elems []Element
}

// Stats reports observable facts about one computation.
type Stats struct {
	// Steps is the number of merge iterations performed. It is bounded
	// by len(a)+len(b), never by the index range, proving no dense
	// expansion happened.
	Steps uint64
	// ExplicitZeros counts elements whose stored Value is exactly 0.
	ExplicitZeros uint64
}
