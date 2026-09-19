package ontology

import "fmt"

// TypeError is returned when a leaf comparison is applied to values whose
// types are not comparable under the operator (e.g. string vs int64, or
// Lt on bools). It identifies the attribute and both operand types.
type TypeError struct {
	Attr     string
	Op       Op
	AttrType string
	LitType  string
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("ontology: cannot compare attribute %q (type %s) with literal (type %s) using %s",
		e.Attr, e.AttrType, e.LitType, e.Op)
}

// DepthError is returned when a predicate tree exceeds the evaluator's
// configured maximum depth.
type DepthError struct {
	Limit int
}

func (e *DepthError) Error() string {
	return fmt.Sprintf("ontology: predicate tree exceeds maximum depth %d", e.Limit)
}
