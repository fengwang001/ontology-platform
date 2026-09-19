package ontology

import (
	"fmt"
	"reflect"
)

// TypeError is a decidable error raised by a comparison leaf when the
// property value and the literal cannot be compared as required, or when
// an ordering operator (Lt/Gt) is applied to a type that only supports
// equality (bool). It is never returned for missing properties or NaN.
type TypeError struct {
	// Property is the left-hand side property name.
	Property string
	// Op is the comparison that failed.
	Op Op
	// LeftType is the runtime type name of the property value.
	LeftType string
	// RightType is the runtime type name of the literal.
	RightType string
	// Reason explains why the comparison is invalid.
	Reason string
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("ontology: type error on property %q for %s: %s",
		e.Property, e.Op, e.Reason)
}

// DepthError is a decidable error returned when a predicate tree is
// deeper than the evaluator's configured limit. It replaces what would
// otherwise be unbounded recursion.
type DepthError struct {
	Limit int
}

func (e *DepthError) Error() string {
	return fmt.Sprintf("ontology: predicate tree exceeds max depth %d", e.Limit)
}

// NodeError reports a predicate node that the evaluator cannot handle.
type NodeError struct {
	Node Predicate
}

func (e *NodeError) Error() string {
	return fmt.Sprintf("ontology: unsupported predicate node %T", e.Node)
}

func typeName(v any) string {
	if v == nil {
		return "null"
	}
	return reflect.TypeOf(v).String()
}
