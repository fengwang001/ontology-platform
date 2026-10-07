package ontology

import "fmt"

// DeterminationError distinguishes the mutually exclusive failure classes.
type DeterminationError struct {
	Kind DeterminationErrorKind
	msg  string
}

func (e *DeterminationError) Error() string { return e.msg }

// DeterminationErrorKind enumerates reportable error classes.
type DeterminationErrorKind int

const (
	// ErrRuleSuperseded: the pinned/HEAD rule version was superseded
	// while the request was in flight. Highest priority.
	ErrRuleSuperseded DeterminationErrorKind = iota + 1
	// ErrDanglingReference: the stream references an undeclared link
	// type or an object that has not been created yet.
	ErrDanglingReference
	// ErrBeforeFirstAppearance: requested time precedes the object's
	// creation event.
	ErrBeforeFirstAppearance
	// ErrAmbiguousOrder: imported records share an identical order key.
	ErrAmbiguousOrder
)

// errorPriority defines the single error reported when several apply.
func errorPriority(kind DeterminationErrorKind) int {
	switch kind {
	case ErrRuleSuperseded:
		return 1
	case ErrDanglingReference:
		return 2
	case ErrBeforeFirstAppearance:
		return 3
	case ErrAmbiguousOrder:
		return 4
	default:
		return 100
	}
}

func ruleSupersededf(format string, args ...any) error {
	return &DeterminationError{Kind: ErrRuleSuperseded, msg: fmt.Sprintf(format, args...)}
}

func danglingf(format string, args ...any) error {
	return &DeterminationError{Kind: ErrDanglingReference, msg: fmt.Sprintf(format, args...)}
}

func beforeFirstf(format string, args ...any) error {
	return &DeterminationError{Kind: ErrBeforeFirstAppearance, msg: fmt.Sprintf(format, args...)}
}

func ambiguousf(format string, args ...any) error {
	return &DeterminationError{Kind: ErrAmbiguousOrder, msg: fmt.Sprintf(format, args...)}
}

// AsDeterminationError unwraps a determination error if err is one.
func AsDeterminationError(err error) (*DeterminationError, bool) {
	de, ok := err.(*DeterminationError)
	return de, ok
}

// pickError returns the highest-priority non-nil error among candidates
// in the order they are given (caller orders by priority).
func pickError(candidates ...error) error {
	var best *DeterminationError
	for _, err := range candidates {
		if err == nil {
			continue
		}
		de, ok := err.(*DeterminationError)
		if !ok {
			return err
		}
		if best == nil || errorPriority(de.Kind) < errorPriority(best.Kind) {
			best = de
		}
	}
	if best == nil {
		return nil
	}
	return best
}
