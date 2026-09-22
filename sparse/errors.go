package sparse

import "fmt"

// OrderError reports that a vector's indices are not strictly
// ascending at a given position.
type OrderError struct {
	Vector   int    // 0 = left (first) vector, 1 = right (second)
	Position int    // entry index within that vector
	Prev     uint32 // index stored at Position-1
	Got      uint32 // offending index at Position
}

func (e *OrderError) Error() string {
	return fmt.Sprintf("sparse: vector %d entry %d: index %d not strictly greater than previous %d",
		e.Vector, e.Position, e.Got, e.Prev)
}

// NaNError reports a NaN value at a given position.
type NaNError struct {
	Vector   int // 0 = left, 1 = right
	Position int
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("sparse: vector %d entry %d: value is NaN", e.Vector, e.Position)
}

// ZeroNormError reports that cosine similarity is undefined because
// a vector has zero norm (empty vector or all-zero values).
type ZeroNormError struct {
	Vector int // 0 = left, 1 = right
}

func (e *ZeroNormError) Error() string {
	return fmt.Sprintf("sparse: vector %d has zero norm; cosine undefined", e.Vector)
}

// NonFiniteError reports that a computed result is NaN or +/-Inf,
// e.g. when inputs contain infinities.
type NonFiniteError struct {
	What  string  // "dot" or "cosine"
	Value float64 // the offending non-finite result
}

func (e *NonFiniteError) Error() string {
	return fmt.Sprintf("sparse: %s result is non-finite (%v)", e.What, e.Value)
}
