package predicate

import "fmt"

// TypeError is a decidable error raised when two values cannot be
// compared (for example a string attribute against an int64 literal,
// or an ordering comparison on booleans).
type TypeError struct {
	Attr     string
	Op       Op
	LeftTyp  string
	RightTyp string
}

func (e *TypeError) Error() string {
	return ""
}

// DepthError is a decidable error raised when a predicate tree is
// deeper than the evaluator's configured limit.
type DepthError struct {
	Limit int
}

func (e *DepthError) Error() string {
	return fmt.Sprintf("predicate tree exceeds max depth %d", e.Limit)
}

// NodeError wraps a malformed node (for example a nil child).
type NodeError struct {
	Reason string
}

func (e *NodeError) Error() string {
	return fmt.Sprintf("invalid predicate node: %s", e.Reason)
}
