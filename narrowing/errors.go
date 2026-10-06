package narrowing

import "fmt"

// ErrorClass enumerates the distinguishable analysis failure classes.
// The declaration order defines the reporting priority: invalid
// argument first, then inaccessible property, then missing
// discriminant property, then not assignable.
type ErrorClass int

const (
	// ErrInvalidArg covers malformed types (e.g. duplicate property
	// names in one object), references to undeclared variables and
	// duplicate statement identifiers.
	ErrInvalidArg ErrorClass = iota
	// ErrPropNotAccessible covers discriminant-property conditions on
	// a variable whose current narrowed type has non-object members.
	ErrPropNotAccessible
	// ErrMissingDiscriminant covers discriminant-property conditions
	// where some object member lacks the tested property.
	ErrMissingDiscriminant
	// ErrNotAssignable covers assignments whose type has a member that
	// does not belong to the variable's declared type.
	ErrNotAssignable
)

func (c ErrorClass) String() string {
	switch c {
	case ErrInvalidArg:
		return "invalid argument"
	case ErrPropNotAccessible:
		return "property not accessible"
	case ErrMissingDiscriminant:
		return "missing discriminant property"
	case ErrNotAssignable:
		return "not assignable"
	}
	return "unknown error"
}

// AnalysisError is a single classified analysis failure.
type AnalysisError struct {
	Class  ErrorClass
	StmtID StatementID // empty for program-wide problems
	Var    string      // empty when not tied to one variable
	Msg    string

	order int // pre-order program position, for same-class tie-breaking
	seq   int // evaluation sequence, for tie-breaking within a statement
}

func (e *AnalysisError) Error() string {
	loc := string(e.StmtID)
	if loc == "" {
		loc = "<program>"
	}
	if e.Var != "" {
		return fmt.Sprintf("%s: %s (statement %s, variable %s)", e.Class, e.Msg, loc, e.Var)
	}
	return fmt.Sprintf("%s: %s (statement %s)", e.Class, e.Msg, loc)
}

// errMalformed builds a placeholder invalid-argument error used by the
// type layer; the analyzer re-attaches statement context to it.
func errMalformed(msg string) error {
	return &AnalysisError{Class: ErrInvalidArg, Msg: msg}
}

// errLess reports whether a should be reported before b: class priority
// first, then earliest program order, then evaluation sequence.
func errLess(a, b *AnalysisError) bool {
	if a.Class != b.Class {
		return a.Class < b.Class
	}
	if a.order != b.order {
		return a.order < b.order
	}
	return a.seq < b.seq
}
