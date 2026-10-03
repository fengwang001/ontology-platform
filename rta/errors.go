package rta

// RejectReason identifies the first applicable rejection cause.
type RejectReason int

const (
	ReasonInvalidParam RejectReason = iota + 1
	ReasonDuplicate
	ReasonCapacityFull
	ReasonUnschedulable
	ReasonNotFound
)

// Error reports an operation rejection. For ReasonUnschedulable, Pending is
// the number of tasks not yet assigned at the failing Audsley level
// (including the level being filled).
type Error struct {
	Reason  RejectReason
	Pending int
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

func newError(reason RejectReason, pending int, msg string) *Error {
	return &Error{Reason: reason, Pending: pending, Message: msg}
}
