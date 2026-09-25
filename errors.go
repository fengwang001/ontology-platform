package ontology

import (
	"errors"
	"fmt"
)

// TypeError reports an incomparable pair of operand types in a leaf
// comparison. It is a decidable error: evaluation stops and the error
// is returned to the caller.
type TypeError struct {
	Attr      string
	Op        Op
	AttrType  string
	ValueType string
	Reason    string
}

// Error implements the error interface.
func (e *TypeError) Error() string {
	return fmt.Sprintf("ontology: type error on attribute %q (%s): cannot compare %s with %s",
		e.Attr, e.Op, e.AttrType, e.ValueType)
}

// IsTypeError reports whether err is (or wraps) a *TypeError.
func IsTypeError(err error) bool {
	var te *TypeError
	return errors.As(err, &te)
}

// DepthError reports that a predicate tree exceeds the evaluator's
// configured maximum depth.
type DepthError struct {
	MaxDepth int
}

// Error implements the error interface.
func (e *DepthError) Error() string {
	return fmt.Sprintf("ontology: predicate tree exceeds maximum depth %d", e.MaxDepth)
}

// IsDepthError reports whether err is (or wraps) a *DepthError.
func IsDepthError(err error) bool {
	var de *DepthError
	return errors.As(err, &de)
}
