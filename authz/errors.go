package authz

import "fmt"

// ErrorKind distinguishes the two error categories. Priority, high to low:
// ErrKindInvalidArgument, then ErrKindInvalidPolicySet.
type ErrorKind int

const (
	// ErrKindInvalidArgument: the request itself is malformed.
	ErrKindInvalidArgument ErrorKind = iota
	// ErrKindInvalidPolicySet: a submitted policy set is malformed.
	ErrKindInvalidPolicySet
)

func (k ErrorKind) String() string {
	if k == ErrKindInvalidArgument {
		return "invalid argument"
	}
	return "invalid policy set"
}

// Error is the single error type returned by this package; Kind selects the
// category. For policy-set errors, PolicyIndex is the index of the offending
// policy in submission order and Policy its name (when available).
type Error struct {
	Kind        ErrorKind
	Field       string
	Message     string
	PolicyIndex int
	Policy      string
}

func (e *Error) Error() string {
	if e.Kind == ErrKindInvalidPolicySet {
		return fmt.Sprintf("%s: policy[%d] %q: %s: %s", e.Kind, e.PolicyIndex, e.Policy, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s: %s", e.Kind, e.Field, e.Message)
}

func argErr(field, msg string) *Error {
	return &Error{Kind: ErrKindInvalidArgument, Field: field, Message: msg, PolicyIndex: -1}
}

func policyErr(index int, name, field, msg string) *Error {
	return &Error{Kind: ErrKindInvalidPolicySet, Field: field, Message: msg, PolicyIndex: index, Policy: name}
}
