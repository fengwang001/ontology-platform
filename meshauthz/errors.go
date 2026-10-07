package meshauthz

import "fmt"

// ErrorCategory classifies failures so callers can distinguish them.
//
// Categories have a strict priority, from high to low:
// ErrCategoryInvalidArgument (malformed request fields) outranks
// ErrCategoryInvalidPolicySet (rejected policy-set replacement).
// Evaluation validates the request before touching any policy state,
// so when both could apply the higher-priority category is reported.
type ErrorCategory int

const (
	// ErrCategoryInvalidArgument is the highest-priority category:
	// a request field is illegal.
	ErrCategoryInvalidArgument ErrorCategory = iota
	// ErrCategoryInvalidPolicySet means a policy-set replacement was
	// rejected as a whole; the installed version is unchanged.
	ErrCategoryInvalidPolicySet
)

// String renders the category in a stable, log-friendly form.
func (c ErrorCategory) String() string {
	switch c {
	case ErrCategoryInvalidArgument:
		return "invalid_argument"
	case ErrCategoryInvalidPolicySet:
		return "invalid_policy_set"
	}
	return "unknown"
}

// Error is the single error type returned by this package. Category
// is always populated; PolicyIndex is the submission-order index of
// the first offending policy for ErrCategoryInvalidPolicySet, or -1.
type Error struct {
	Category    ErrorCategory
	PolicyIndex int
	Message     string
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Category == ErrCategoryInvalidPolicySet && e.PolicyIndex >= 0 {
		return fmt.Sprintf("%s: policy[%d]: %s", e.Category, e.PolicyIndex, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Category, e.Message)
}

func invalidArgumentf(format string, args ...any) *Error {
	return &Error{
		Category:    ErrCategoryInvalidArgument,
		PolicyIndex: -1,
		Message:     fmt.Sprintf(format, args...),
	}
}

func invalidPolicySetf(index int, format string, args ...any) *Error {
	return &Error{
		Category:    ErrCategoryInvalidPolicySet,
		PolicyIndex: index,
		Message:     fmt.Sprintf(format, args...),
	}
}
