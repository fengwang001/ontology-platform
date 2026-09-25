package sparse

import "fmt"

// ValidationError reports a malformed input vector, located by vector
// number (0 = first argument, 1 = second) and element position.
type ValidationError struct {
	Vector   int
	Position int
	Reason   string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("sparse: vector %d element %d: %s", e.Vector, e.Position, e.Reason)
}

// NumericError reports a non-finite (Inf or NaN) intermediate or final
// result, e.g. a dot product involving infinite values.
type NumericError struct {
	Op     string
	Detail string
}

func (e *NumericError) Error() string {
	return fmt.Sprintf("sparse: %s: %s", e.Op, e.Detail)
}

// ZeroNormError is returned by Cosine when a vector has zero norm
// (empty or all-zero), where cosine similarity is undefined.
type ZeroNormError struct {
	Vector int
}

func (e *ZeroNormError) Error() string {
	return fmt.Sprintf("sparse: vector %d has zero norm; cosine undefined", e.Vector)
}
