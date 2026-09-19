package ontology

import "fmt"

// TypeError reports an incomparable pair of operand types at a leaf.
// It names the attribute and the concrete types of both sides.
type TypeError struct {
	Attr      string
	Op        Op
	AttrType  string
	ValueType string
	Reason    string
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("type error at attribute %q: cannot apply %s to %s and %s (%s)",
		e.Attr, e.Op, e.AttrType, e.ValueType, e.Reason)
}

// DepthError reports that a predicate tree exceeds the configured
// maximum evaluation depth.
type DepthError struct {
	Limit int
}

func (e *DepthError) Error() string {
	return fmt.Sprintf("predicate tree exceeds maximum depth %d", e.Limit)
}
