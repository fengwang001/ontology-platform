package lifecycle

import (
	"fmt"
	"strings"
)

type ErrorKind string

const (
	KindGuardBlocked    ErrorKind = "time_guard_blocked"
	KindCascadeFailed   ErrorKind = "cascade_failed"
	KindActionDenied    ErrorKind = "action_precondition_failed"
	KindClockRegression ErrorKind = "clock_regression"
)

// Priority returns the fixed error priority: lower numbers win (1 is highest).
// Ordering:
//  1. a time-transition in the lazy chain whose non-temporal guard fails,
//  2. a cross-instance cascade that could not be forced,
//  3. an explicit action whose precondition still fails after settlement,
//  4. an anomaly produced by a clock regression.
func (k ErrorKind) Priority() int {
	switch k {
	case KindGuardBlocked:
		return 1
	case KindCascadeFailed:
		return 2
	case KindActionDenied:
		return 3
	case KindClockRegression:
		return 4
	default:
		return 5
	}
}

type Error struct {
	Kind       ErrorKind
	Instance   InstanceID
	Transition TransitionID
	Op         string
	Detail     string
}

func (e *Error) Error() string {
	return fmt.Sprintf("lifecycle %s: instance=%s transition=%s op=%s: %s",
		e.Kind, e.Instance, e.Transition, e.Op, e.Detail)
}

type ErrorList []*Error

// Primary returns the error with the highest fixed priority and nil for an
// empty list. Ties keep the first occurrence (settlement order is causal).
func (l ErrorList) Primary() *Error {
	var primary *Error
	for _, e := range l {
		if e == nil {
			continue
		}
		if primary == nil || e.Kind.Priority() < primary.Kind.Priority() {
			primary = e
		}
	}
	return primary
}

func (l ErrorList) Error() string {
	if len(l) == 0 {
		return ""
	}
	parts := make([]string, 0, len(l))
	for _, e := range l {
		if e != nil {
			parts = append(parts, e.Error())
		}
	}
	return strings.Join(parts, "; ")
}

func (l ErrorList) Has(kind ErrorKind) bool {
	for _, e := range l {
		if e != nil && e.Kind == kind {
			return true
		}
	}
	return false
}
