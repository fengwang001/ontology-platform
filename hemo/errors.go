// Package hemo implements the chair scheduling and infection-isolation
// engine of a hemodialysis center.
package hemo

// ErrCode identifies a reject reason. Codes are ordered by the report
// priority mandated by the specification: when more than one rule would
// reject an operation, the code with the smallest ordinal is reported.
type ErrCode int

const (
	// ErrInvalidArgument: empty identifier, non-positive duration,
	// out-of-range timestamp, malformed weekday mask, etc.
	ErrInvalidArgument ErrCode = iota + 1
	// ErrClockRollback: operation now is earlier than the last accepted now.
	ErrClockRollback
	// ErrNotFound: referenced chair, patient, plan or treatment is unknown.
	ErrNotFound
	// ErrStateConflict: object is in a state incompatible with the operation,
	// e.g. cancelling an already started treatment.
	ErrStateConflict
	// ErrIsolationConflict: infection-status / zone / observation /
	// deep-disinfection rule violation.
	ErrIsolationConflict
	// ErrPatientConflict: two treatments of the same patient overlap or are
	// closer than the minimum recovery interval.
	ErrPatientConflict
	// ErrNoFeasibleChair: no chair can host one of the required treatments.
	ErrNoFeasibleChair
)

// Error implements the error interface.
func (c ErrCode) Error() string {
	switch c {
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrClockRollback:
		return "clock rollback"
	case ErrNotFound:
		return "object not found"
	case ErrStateConflict:
		return "state conflict"
	case ErrIsolationConflict:
		return "infection isolation conflict"
	case ErrPatientConflict:
		return "patient schedule conflict"
	case ErrNoFeasibleChair:
		return "no feasible chair"
	default:
		return "unknown error"
	}
}

// OpError carries a stable error code together with a human readable detail.
type OpError struct {
	Code   ErrCode
	Detail string
}

func (e *OpError) Error() string {
	if e.Detail == "" {
		return e.Code.Error()
	}
	return e.Code.Error() + ": " + e.Detail
}
