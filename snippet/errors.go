// Package snippet implements a document registry with deterministic
// fixed-window greedy snippet selection over byte-offset hit intervals.
package snippet

// RejectReason is the machine-readable reason an operation was rejected.
type RejectReason string

const (
	ReasonInvalidArgument RejectReason = "invalid_argument"
	ReasonDuplicateDoc    RejectReason = "duplicate_document"
	ReasonDocNotFound     RejectReason = "document_not_found"
	ReasonInvalidHit      RejectReason = "invalid_hit"
)

// RejectError reports a rejected operation. Rejected operations never mutate
// the registry.
type RejectError struct {
	Reason  RejectReason
	Message string
}

func (e *RejectError) Error() string {
	return "snippet: " + string(e.Reason) + ": " + e.Message
}
