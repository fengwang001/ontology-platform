package ontology

import "fmt"

type ErrorKind string

const (
	ErrInvalid       ErrorKind = "invalid"
	ErrNotFound      ErrorKind = "not_found"
	ErrConflict      ErrorKind = "conflict"
	ErrViolation     ErrorKind = "violation"
	ErrLimitExceeded ErrorKind = "limit_exceeded"
)

type RBACError struct {
	Kind           ErrorKind
	ObjectKind     string
	ObjectName     string
	ConstraintName string
	ImplicitBy     []string
	Reason         string
}

func (e *RBACError) Error() string {
	if e == nil {
		return ""
	}
	if e.ConstraintName != "" {
		return fmt.Sprintf("%s: %s %q violates constraint %q", e.Kind, e.ObjectKind, e.ObjectName, e.ConstraintName)
	}
	if e.ObjectKind != "" {
		return fmt.Sprintf("%s: %s %q: %s", e.Kind, e.ObjectKind, e.ObjectName, e.Reason)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Reason)
}

func invalid(kind, name, reason string) *RBACError {
	return &RBACError{Kind: ErrInvalid, ObjectKind: kind, ObjectName: name, Reason: reason}
}

func notFound(kind, name string) *RBACError {
	return &RBACError{Kind: ErrNotFound, ObjectKind: kind, ObjectName: name, Reason: "does not exist"}
}

func conflict(kind, name, reason string, implicitBy ...string) *RBACError {
	return &RBACError{Kind: ErrConflict, ObjectKind: kind, ObjectName: name, Reason: reason, ImplicitBy: implicitBy}
}

func violation(kind, objectName, constraintName string) *RBACError {
	return &RBACError{Kind: ErrViolation, ObjectKind: kind, ObjectName: objectName, ConstraintName: constraintName}
}

func limitExceeded(kind, name, reason string) *RBACError {
	return &RBACError{Kind: ErrLimitExceeded, ObjectKind: kind, ObjectName: name, Reason: reason}
}

func IsInvalid(err error) bool       { return errorKind(err) == ErrInvalid }
func IsNotFound(err error) bool      { return errorKind(err) == ErrNotFound }
func IsConflict(err error) bool      { return errorKind(err) == ErrConflict }
func IsViolation(err error) bool     { return errorKind(err) == ErrViolation }
func IsLimitExceeded(err error) bool { return errorKind(err) == ErrLimitExceeded }

func errorKind(err error) ErrorKind {
	if e, ok := err.(*RBACError); ok {
		return e.Kind
	}
	return ""
}
