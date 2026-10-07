// Package ontology implements an incremental aggregation-view subsystem for the
// ontology platform.
package ontology

// ErrorClass identifies a fixed category of failure. Classes are reported in a
// fixed priority order when several conditions hold simultaneously: callers
// choose the error with the smallest Priority value.
type ErrorClass int

const (
	// ClassNone is not an error.
	ClassNone ErrorClass = 0

	// ClassGroupMissing (priority 1): the grouping instance referenced by an
	// operation does not exist (or was deleted).
	ClassGroupMissing ErrorClass = 100

	// ClassTypeNotParticipating (priority 2): the aggregated object type is not
	// declared as a participant of the target view.
	ClassTypeNotParticipating ErrorClass = 90

	// ClassPolicyRejected (priority 3): the view definition's membership policy
	// forbids the requested configuration (e.g. a second simultaneous
	// membership under a DenyMulti view).
	ClassPolicyRejected ErrorClass = 85

	// ClassConcurrentConflict (priority 4): an optimistic-concurrency conflict
	// caused an ownership change to be rejected; no aggregate state changed.
	ClassConcurrentConflict ErrorClass = 80

	// ClassTxnFailed (priority 5): an update inside an atomic processing unit
	// failed, so the whole unit was rolled back.
	ClassTxnFailed ErrorClass = 70

	// ClassInvalid is a generic malformed-request error below the fixed
	// classes.
	ClassInvalid ErrorClass = 10
)

// Priority reports the fixed reporting priority; smaller wins.
func (c ErrorClass) Priority() int { return int(c) }

// OpError is a classified operation error.
type OpError struct {
	Class ErrorClass
	Op    string
	Msg   string
}

func (e *OpError) Error() string {
	if e.Op != "" {
		return e.Op + ": " + e.Msg
	}
	return e.Msg
}

func classified(class ErrorClass, op, msg string) *OpError {
	return &OpError{Class: class, Op: op, Msg: msg}
}
