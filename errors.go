package predicate

import "fmt"

// ErrorKind classifies a determinate evaluation error.
type ErrorKind uint8

const (
	// KindTypeMismatch means the attribute value and the literal cannot be
	// compared with the requested operator (including ordering a bool).
	KindTypeMismatch ErrorKind = iota + 1
	// KindUnsupportedType means one side holds a Go type the evaluator does
	// not know how to compare at all.
	KindUnsupportedType
	// KindDepthExceeded means the predicate is deeper than the configured
	// limit.
	KindDepthExceeded
	// KindMalformed means the predicate tree is structurally invalid, for
	// example an empty conjunction.
	KindMalformed
)

// EvalError is a determinate error: it is never Unknown and aborts the whole
// evaluation unless it lives in a subtree that short-circuiting skipped.
type EvalError struct {
	Kind ErrorKind
	// Attr is the attribute name involved; empty for structural errors.
	Attr string
	// AttrType / LiteralType describe the Go types on each side. Use the
	// name "<missing>" for an absent attribute and "<nil>" for nil.
	AttrType    string
	LiteralType string
	// Op is the comparison operator; 0 for non-comparison errors.
	Op  Op
	msg string
}

func (e *EvalError) Error() string { return e.msg }

func typeMismatch(attr string, lhs, rhs any, op Op) *EvalError {
	return &EvalError{
		Kind:        KindTypeMismatch,
		Attr:        attr,
		AttrType:    goTypeName(lhs),
		LiteralType: goTypeName(rhs),
		Op:          op,
		msg: fmt.Sprintf("predicate: attribute %q of type %s is not comparable with %s to literal of type %s",
			attr, goTypeName(lhs), op, goTypeName(rhs)),
	}
}

func unsupportedType(attr string, v any, op Op) *EvalError {
	return &EvalError{
		Kind:        KindUnsupportedType,
		Attr:        attr,
		AttrType:    goTypeName(v),
		LiteralType: goTypeName(nil),
		Op:          op,
		msg:         fmt.Sprintf("predicate: attribute %q has unsupported type %s", attr, goTypeName(v)),
	}
}

func depthExceeded(limit, reached int) *EvalError {
	return &EvalError{
		Kind: KindDepthExceeded,
		msg:  fmt.Sprintf("predicate: depth %d exceeds configured limit %d", reached, limit),
	}
}

func malformed(reason string) *EvalError {
	return &EvalError{
		Kind: KindMalformed,
		msg:  "predicate: malformed tree: " + reason,
	}
}
