package ontology

import "errors"

// ErrorKind classifies every rejected operation.
type ErrorKind string

const (
	ErrInvalidArgument ErrorKind = "invalid_argument"
	ErrNotFound        ErrorKind = "not_found"
	ErrDeactivated     ErrorKind = "reviewer_deactivated"
	ErrTaskState       ErrorKind = "task_state"
	ErrScore           ErrorKind = "score_invalid"
	ErrNoCandidate     ErrorKind = "no_candidate"
)

// EngineError carries the fixed-priority classification.
type EngineError struct {
	Kind ErrorKind
	Msg  string
	// RejectReasons, only for ErrNoCandidate, reports how many otherwise
	// eligible reviewers were blocked by each constraint (deterministic).
	RejectReasons map[string]int
}

func (e *EngineError) Error() string { return string(e.Kind) + ": " + e.Msg }

func mkErr(kind ErrorKind, msg string) *EngineError {
	return &EngineError{Kind: kind, Msg: msg}
}

// KindOf extracts the ErrorKind, or "" when err is nil or non-engine.
func KindOf(err error) ErrorKind {
	var ee *EngineError
	if errors.As(err, &ee) {
		return ee.Kind
	}
	return ""
}
