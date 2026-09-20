package ontology

import "errors"

// ErrNonPositiveCapacity is returned by New when k <= 0.
var ErrNonPositiveCapacity = errors.New("ontology: reservoir capacity k must be positive")

// ErrInvalidWeight is returned by Add when the weight is not a positive
// integer (zero, negative, non-integer, NaN, Inf, or overflowing int64).
// The element is rejected and never enters the reservoir.
var ErrInvalidWeight = errors.New("ontology: weight must be a positive integer")
