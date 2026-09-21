// Package sparsevec computes dot products and cosine similarity over
// sparse vectors without ever expanding them into dense form.
package sparsevec

// Element is a single (index, value) pair of a sparse vector.
type Element struct {
	Index uint32
	Value float64
}

// Vector is a sparse vector: elements sorted by strictly ascending Index.
// Explicit zero values are allowed and counted, but never affect results.
type Vector []Element

// Stats reports observable facts about a single computation.
type Stats struct {
	// Steps is how many merge iterations the two-pointer scan performed.
	Steps int
	// ZerosA / ZerosB count explicit zero-valued elements per input vector.
	ZerosA int
	ZerosB int
}

// TotalZeros is the number of explicit zero elements across both inputs.
func (s Stats) TotalZeros() int { return s.ZerosA + s.ZerosB }
