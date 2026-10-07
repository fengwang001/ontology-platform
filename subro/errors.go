package subro

import (
	"errors"
	"fmt"
)

// ErrKind classifies rejection reasons. The declaration order is the
// reporting priority: when several reasons apply, only the first one
// (smallest kind) is reported.
type ErrKind int

const (
	ErrInvalidParam ErrKind = iota
	ErrClockRollback
	ErrCaseNotFound
	ErrPastDeadline
	ErrExceedsTotalLoss
	ErrAlreadyWaived
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid_param"
	case ErrClockRollback:
		return "clock_rollback"
	case ErrCaseNotFound:
		return "case_not_found"
	case ErrPastDeadline:
		return "past_deadline"
	case ErrExceedsTotalLoss:
		return "exceeds_total_loss"
	case ErrAlreadyWaived:
		return "already_waived"
	}
	return "unknown"
}

// Error is the single error type returned by all operations.
type Error struct {
	Kind   ErrKind
	Op     OpKind
	CaseID string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s rejected: %s: case %q: %s", e.Op, e.Kind, e.CaseID, e.Detail)
}

// IsKind reports whether err is a *Error with the given kind.
func IsKind(err error, kind ErrKind) bool {
	var se *Error
	if errors.As(err, &se) {
		return se.Kind == kind
	}
	return false
}

func newError(kind ErrKind, op OpKind, caseID, detail string) *Error {
	return &Error{Kind: kind, Op: op, CaseID: caseID, Detail: detail}
}
