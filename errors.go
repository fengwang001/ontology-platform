// Package ontology computes dot products and cosine similarity over
// sparse vectors stored in process memory.
//
// A sparse vector is a sequence of (index, value) pairs sorted by
// strictly ascending index. All computation is a single two-pointer
// merge; vectors are never expanded into dense arrays and never
// modified. Only the standard library is used.
package ontology

import "fmt"

// Side identifies which input vector of a call an error refers to.
type Side int

const (
	// First is the first vector argument.
	First Side = iota + 1
	// Second is the second vector argument.
	Second
	// Both means the condition applies to both vectors.
	Both
)

func (s Side) String() string {
	switch s {
	case First:
		return "first"
	case Second:
		return "second"
	default:
		return "both"
	}
}

// OrderError reports an element whose index is not strictly greater
// than the index of the preceding element.
type OrderError struct {
	Side Side  // which vector
	Pos  int   // position of the offending element (0-based)
	Prev uint32 // index of the previous element
	Got  uint32 // index of the offending element
}

func (e *OrderError) Error() string {
	return fmt.Sprintf("ontology: %s vector element %d: index %d is not strictly greater than previous index %d",
		e.Side, e.Pos, e.Got, e.Prev)
}

// NaNError reports an element whose value is NaN.
type NaNError struct {
	Side Side // which vector
	Pos  int  // position of the offending element (0-based)
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("ontology: %s vector element %d: value is NaN", e.Side, e.Pos)
}

// InfError reports an element whose value is +Inf or -Inf. Products
// involving infinities can yield Inf or NaN, so they are rejected up
// front instead of leaking a NaN to the caller.
type InfError struct {
	Side  Side    // which vector
	Pos   int     // position of the offending element (0-based)
	Value float64 // the offending value (+Inf or -Inf)
}

func (e *InfError) Error() string {
	return fmt.Sprintf("ontology: %s vector element %d: value is infinite (%v)", e.Side, e.Pos, e.Value)
}

// ZeroNormError is returned by Cosine when a vector has zero norm
// (empty vector or all values zero); cosine is undefined there.
type ZeroNormError struct {
	Side Side // First, Second, or Both
}

func (e *ZeroNormError) Error() string {
	return fmt.Sprintf("ontology: %s vector has zero norm; cosine is undefined", e.Side)
}
