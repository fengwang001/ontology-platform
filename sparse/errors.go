package sparse

import "fmt"

// OrderError reports a vector whose indices are not strictly
// increasing. Use errors.As to inspect it.
type OrderError struct {
	Vector   int // 0 = first argument, 1 = second argument
	Position int // position within that vector where the violation occurs
	Prev     uint32
	Cur      uint32
}

func (e *OrderError) Error() string {
	return fmt.Sprintf("sparse: vector %d: index not strictly increasing at position %d (prev=%d, cur=%d)",
		e.Vector, e.Position, e.Prev, e.Cur)
}

// NaNError reports a NaN value in an input vector.
type NaNError struct {
	Vector   int // 0 = first argument, 1 = second argument
	Position int // position within that vector
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("sparse: vector %d: NaN value at position %d", e.Vector, e.Position)
}

// ZeroNormError reports a cosine computation where one vector has zero
// norm (empty vector or all values zero), so the cosine is undefined.
type ZeroNormError struct {
	Vector int // 0 = first argument, 1 = second argument
}

func (e *ZeroNormError) Error() string {
	return fmt.Sprintf("sparse: vector %d: zero norm, cosine undefined", e.Vector)
}

// NonFiniteError reports a computation whose result would be NaN or
// +/-Inf (e.g. inputs containing infinities, or overflow). The
// non-finite value is never handed back as a valid result.
type NonFiniteError struct {
	Context string  // "dot" or "cosine"
	Value   float64 // the offending non-finite value
}

func (e *NonFiniteError) Error() string {
	return fmt.Sprintf("sparse: %s result is non-finite (%v)", e.Context, e.Value)
}
