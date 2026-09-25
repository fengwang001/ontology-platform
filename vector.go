// Package sparse computes dot products and cosine similarities of sparse
// vectors via a single two-pointer merge, without densifying them.
package sparse

// Element is one nonzero (or explicitly zero) entry of a sparse vector.
// Elements of a Vector must appear in strictly ascending Index order.
type Element struct {
	Index uint32
	Value float64
}

// Vector is a sparse vector: elements sorted by strictly ascending Index.
// Explicit zero values are permitted; they carry no weight but are counted.
type Vector []Element

// Stats reports observable facts about one computation.
type Stats struct {
	// Steps is the number of merge iterations performed. It is bounded by
	// len(a)+len(b), never by the index space, so vectors indexed up to 1e9
	// still cost only a handful of steps.
	Steps uint64
	// ExplicitZeros counts elements with Value == 0 across both inputs.
	ExplicitZeros uint64
}
