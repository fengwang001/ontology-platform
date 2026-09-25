// Package sparse computes dot products and cosine similarities of
// sparse vectors without ever expanding them into dense arrays.
//
// A sparse vector is a sequence of (index, value) pairs whose indices are
// required to be in strictly ascending order. Explicit zero values are
// legal and are counted separately.
package sparse

import "errors"

// Element is one non-sparse coordinate of a vector.
type Element struct {
	Index uint32
	Value float64
}

// Vector is a sparse vector: a slice of elements with strictly ascending
// indices. Callers must not mutate a Vector while it is being used.
type Vector []Element

// Stats reports observable facts about a single merge computation.
type Stats struct {
	// Steps counts how many iterations the two-pointer merge advanced,
	// i.e. how many leading elements were consumed from the two vectors.
	// For two vectors of n and m elements, Steps <= n+m, regardless of how
	// large the indices are.
	Steps int

	// ExplicitZeros counts elements whose value is exactly zero in the two
	// input vectors. Storing explicit zeros is legal.
	ExplicitZeros int
}

// Result is the outcome of a dot product computation.
type Result struct {
	Value float64
	Stats Stats
}

// VectorID identifies one of the two input vectors.
type VectorID int

const (
	VectorA VectorID = 1
	VectorB VectorID = 2
)

func (v VectorID) String() string {
	switch v {
	case VectorA:
		return "vector A (argument 1)"
	case VectorB:
		return "vector B (argument 2)"
	default:
		return "unknown vector"
	}
}

// Reason categorises a computation error so callers can decide with errors.Is.
type Reason int

const (
	ReasonNonStrictOrder  Reason = iota // equal or decreasing index
	ReasonNaN                           // element value is NaN
	ReasonInf                           // element value is +/-Inf
	ReasonZeroNorm                      // cosine of a zero-norm vector requested
	ReasonNonFiniteResult               // dot product/result overflowed to Inf or NaN
)

// Error is the typed, decidable error returned by this package.
type Error struct {
	Reason   Reason
	Vector   VectorID // 0 when the error is not specific to one vector
	Position int      // 1-based position within Vector; 0 when not applicable
	Msg      string
}

func (e *Error) Error() string { return e.Msg }

// Sentinel errors for errors.Is decisions.
var (
	ErrNonStrictOrder  = errors.New("sparse: indices must be strictly ascending")
	ErrNaNValue        = errors.New("sparse: NaN element value")
	ErrInfValue        = errors.New("sparse: infinite element value")
	ErrZeroNorm        = errors.New("sparse: cosine undefined for zero-norm vector")
	ErrNonFiniteResult = errors.New("sparse: non-finite result")
)

func (e *Error) Unwrap() error {
	switch e.Reason {
	case ReasonNonStrictOrder:
		return ErrNonStrictOrder
	case ReasonNaN:
		return ErrNaNValue
	case ReasonInf:
		return ErrInfValue
	case ReasonZeroNorm:
		return ErrZeroNorm
	case ReasonNonFiniteResult:
		return ErrNonFiniteResult
	default:
		return nil
	}
}
